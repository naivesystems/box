package buildbot

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"naive.systems/box/portal/gerrit"
)

type worker struct {
	name     string
	password string
}

type Buildbot struct {
	// Absolute path to a directory where Buildbot stores its mutable state
	WorkDir string

	// Absolute path to the buildbot binary (optional, default: '${WorkDir}/sandbox/bin/buildbot')
	BinPath string

	// Absolute path to the buildbot-worker binary (optional, default: '${WorkDir}/sandbox/bin/buildbot-worker')
	WorkerBin string

	dbURL string

	EnvPATH string

	// Absolute path to .ssh/id_ed25519
	IdentityFile string

	pbHost string
	pbPort int

	// A comma-separated list of worker names and passwords (e.g. 'worker1,pass1,worker2,pass2')
	WorkersList string

	WWWProtocol string
	WWWHost     string
	wwwPort     int
	PublicPort  int

	workers []worker

	Gerrit struct {
		Server string
		Port   int
	}
}

func New() *Buildbot {
	return &Buildbot{
		dbURL:       "sqlite:///state.sqlite",
		EnvPATH:     "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin",
		pbHost:      "127.0.0.1",
		pbPort:      9989,
		WWWProtocol: "http",
		WWWHost:     "127.0.0.1",
		wwwPort:     8010,
		PublicPort:  8010,
	}
}

func (bb *Buildbot) parseWorkersList() error {
	parts := strings.Split(bb.WorkersList, ",")
	if len(parts)%2 != 0 {
		return fmt.Errorf("--buildbot_workers is invalid because it has %d parts", len(parts))
	}
	for i := 0; i < len(parts); i += 2 {
		w := worker{strings.TrimSpace(parts[i]), parts[i+1]}
		if w.name == "" {
			return fmt.Errorf("--buildbot_workers has an empty worker name @%d", i)
		}
		d := filepath.Join(bb.WorkDir, w.name)
		fi, err := os.Stat(d)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("--buildbot_workers: '%s' is not a directory", d)
			}
		} else {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("--buildbot_workers: os.Stat('%s'): %v", d, err)
			}
		}
		bb.workers = append(bb.workers, w)
	}
	return nil
}

func (bb *Buildbot) purgeWorkers() error {
	for _, w := range bb.workers {
		d := filepath.Join(bb.WorkDir, w.name)
		log.Printf("rm -rf %s", d)
		err := os.RemoveAll(d)
		if err != nil {
			return fmt.Errorf("os.RemoveAll('%s'): %v", d, err)
		}
	}
	return nil
}

func (bb *Buildbot) createWorkers() error {
	masterURL := fmt.Sprintf("%s:%d", bb.pbHost, bb.pbPort)
	for _, w := range bb.workers {
		cmd := exec.Command(bb.WorkerBin, "create-worker", w.name, masterURL, w.name, w.password)
		cmd.Dir = bb.WorkDir
		out, err := cmd.CombinedOutput()
		log.Printf("%s create-worker %s %s %s <passwd>\n%s", bb.WorkerBin, w.name, masterURL, w.name, out)
		if err != nil {
			return fmt.Errorf("buildbot-worker create-worker %s: %v", w.name, err)
		}
	}
	return nil
}

func (bb *Buildbot) writeConfig(w io.Writer, gps []*gerrit.Project) {
	fmt.Fprint(w, `from buildbot.plugins import *
from buildbot.reporters.gerrit import GerritStatusPush
from buildbot.reporters.http import HttpStatusPush
from buildbot.reporters.generators.build import BuildStatusGenerator
from buildbot.reporters.message import MessageFormatterFunction

c = BuildmasterConfig = {}
c['buildbotNetUsageData'] = None
`)
	fmt.Fprintf(w, "c['buildbotURL'] = '%s://%s:%d/'\n", bb.WWWProtocol, bb.WWWHost, bb.PublicPort)
	fmt.Fprintf(w, "c['db'] = {'db_url': '%s'}\n", bb.dbURL)
	fmt.Fprintf(w, "c['protocols'] = {'pb': {'port': 'tcp:%d:interface=%s'}}\n", bb.pbPort, bb.pbHost)
	fmt.Fprint(w, "c['title'] = 'Buildbot'\n")
	fmt.Fprintf(w, "c['titleURL'] = '%s://%s:%d/'\n", bb.WWWProtocol, bb.WWWHost, bb.PublicPort)
	fmt.Fprintf(w, `
c['www'] = dict(port="tcp:%d:interface=%s",
                auth=util.RemoteUserAuth(header="X-Remote-User",
                                         headerRegex="(?P<username>.+)"),
                plugins={'base_react': {}},
                change_hook_dialects={'gitlab': True, 'github': {}})
`, bb.wwwPort, bb.pbHost)
	fmt.Fprint(w, `
c['change_source'] = []
c['schedulers'] = []
c['builders'] = []
c['services'] = []
c['workers'] = []

`)
	for _, worker := range bb.workers {
		fmt.Fprintf(w, "c['workers'].append(worker.Worker('%s', '%s', keepalive_interval=60))\n", worker.name, worker.password)
	}
	if len(gps) == 0 {
		fmt.Fprintf(w, `
c['workers'].append(worker.Worker('dummy', 'dummy'))
factory = util.BuildFactory()
factory.addStep(steps.ShellCommand(command=['/bin/true']))
c['builders'].append(util.BuilderConfig(name='dummy', workername='dummy', factory=factory))
c['schedulers'].append(schedulers.ForceScheduler(name='dummy', builderNames=['dummy']))
`)
	} else {
		fmt.Fprintf(w, `
c['change_source'].append(changes.GerritChangeSource(
	gerritserver='%s',
	gerritport='%d',
	username='buildbot',
	identity_file='%s',
	handled_events=['patchset-created'],
	get_files=True,
	debug=True
))
`, bb.Gerrit.Server, bb.Gerrit.Port, bb.IdentityFile)

		for _, gp := range gps {
			fmt.Fprintf(w, `
c['schedulers'].append(schedulers.AnyBranchScheduler(
	name='%s presubmit scheduler',
	change_filter=util.GerritChangeFilter(
		project='%s',
		eventtype='patchset-created'
	),
	treeStableTimer=None,
	builderNames=['%s presubmit']
))
`, gp.Name, gp.Name, gp.Name)
			fmt.Fprintf(w, `
c['schedulers'].append(schedulers.ForceScheduler(
	name='force',
	builderNames=['%s presubmit']
))
`, gp.Name)
			fmt.Fprintf(w, `
factory = util.BuildFactory()
factory.addStep(steps.Gerrit(
	repourl='ssh://buildbot@%s:%d/%s.git',
	sshPrivateKey=open('%s').read(),
	submodules=True,
	retryFetch=True,
	clobberOnFailure=True,
	mode='full',
	method='fresh'
))
`, bb.Gerrit.Server, bb.Gerrit.Port, gp.ID, bb.IdentityFile)
			fmt.Fprintf(w, `
factory.addStep(steps.TreeSize())
factory.addStep(steps.Compile(
	name='compile',
	command=['make'],
	description='compiling',
	descriptionDone='compiles'
))
factory.addStep(steps.Test(
	name='test',
	command=['make', 'test'],
	description='testing',
	descriptionDone='tests'
))
c['builders'].append(util.BuilderConfig(
	name='%s presubmit',
	`, gp.Name)
			bb.printWorkerNames(w)
			fmt.Fprintf(w, `
	factory=factory,
	env={
		'PATH': '%s',
		'CI': 'true'
	}
))
`, bb.EnvPATH)
		}

		fmt.Fprintf(w, `
c['services'].append(GerritStatusPush(
	server='%s',
	port=%d,
	username='buildbot',
	identity_file='%s'
))
`, bb.Gerrit.Server, bb.Gerrit.Port, bb.IdentityFile)
	}
}

func (bb *Buildbot) PublicKey() (string, error) {
	bytes, err := os.ReadFile(bb.IdentityFile + ".pub")
	if err != nil {
		return "", fmt.Errorf("os.ReadFile('%s.pub'): %v", bb.IdentityFile, err)
	}
	return strings.TrimSpace(string(bytes)), nil
}

func (bb *Buildbot) Start(gps []*gerrit.Project) error {
	if bb.WorkDir == "" {
		return errors.New("--buildbot_workdir must be specified")
	}
	if !filepath.IsAbs(bb.WorkDir) {
		return fmt.Errorf("--buildbot_workdir '%s' is not an absolute path", bb.WorkDir)
	}
	fi, err := os.Stat(bb.WorkDir)
	if err != nil {
		return fmt.Errorf("os.Stat('%s'): %v", bb.WorkDir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("--buildbot_workdir '%s' is not a directory", bb.WorkDir)
	}
	err = bb.parseWorkersList()
	if err != nil {
		return err
	}
	err = bb.purgeWorkers()
	if err != nil {
		return err
	}
	if bb.BinPath == "" {
		bb.BinPath = filepath.Join(bb.WorkDir, "sandbox", "bin", "buildbot")
		log.Printf("--buildbot_bin not set, using default value '%s'", bb.BinPath)
	}
	_, err = os.Stat(bb.BinPath)
	if err != nil {
		return fmt.Errorf("os.Stat('%s'): %v", bb.BinPath, err)
	}
	versionCmd := exec.Command(bb.BinPath, "--version")
	versionOut, err := versionCmd.CombinedOutput()
	log.Printf("%s\n%s", versionCmd, versionOut)
	if err != nil {
		return fmt.Errorf("buildbot --version: %v", err)
	}
	if bb.WorkerBin == "" {
		bb.WorkerBin = filepath.Join(bb.WorkDir, "sandbox", "bin", "buildbot-worker")
		log.Printf("--buildbot_worker_bin not set, using default value '%s'", bb.WorkerBin)
	}
	_, err = os.Stat(bb.WorkerBin)
	if err != nil {
		return fmt.Errorf("os.Stat('%s'): %v", bb.WorkerBin, err)
	}
	if bb.IdentityFile == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home dir: %v", err)
		}
		bb.IdentityFile = filepath.Join(homeDir, ".ssh", "id_ed25519")
		log.Printf("--buildbot_identity_file not set, using default value '%s'", bb.IdentityFile)
	}
	_, err = os.Stat(bb.IdentityFile)
	if err != nil {
		return fmt.Errorf("os.Stat('%s'): %v", bb.IdentityFile, err)
	}
	_, err = os.Stat(bb.IdentityFile + ".pub")
	if err != nil {
		return fmt.Errorf("os.Stat('%s.pub'): %v", bb.IdentityFile, err)
	}
	err = bb.createWorkers()
	if err != nil {
		return err
	}
	err = bb.RewriteConfig(gps)
	if err != nil {
		return err
	}
	err = bb.startMaster()
	if err != nil {
		return err
	}
	err = bb.startWorkers()
	if err != nil {
		e := bb.stopMaster()
		if e != nil {
			log.Printf("%v", e)
		}
		return err
	}
	return nil
}

// See also: b/12603
func (bb *Buildbot) cleanupTwistdPidFile(nodeName string) {
	twistdPidFile := filepath.Join(bb.WorkDir, nodeName, "twistd.pid")
	fi, err := os.Stat(twistdPidFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("os.Stat('%s'): %v", twistdPidFile, err)
		}
		return
	}
	if fi.Size() == 0 {
		log.Printf("%s exists but is empty", twistdPidFile)
		err := os.Remove(twistdPidFile)
		if err != nil {
			log.Printf("os.Remove('%s'): %v", twistdPidFile, err)
		}
	} else {
		bytes, err := os.ReadFile(twistdPidFile)
		if err != nil {
			log.Printf("os.ReadFile('%s'): %v", twistdPidFile, err)
			return
		}
		pid, err := strconv.Atoi(string(bytes))
		if err != nil {
			log.Printf("failed to convert '%s' to int: %v", string(bytes), err)
			return
		}
		err = syscall.Kill(pid, syscall.SIGKILL)
		if err != nil {
			log.Printf("failed to kill '%d': %v", pid, err)
		}
	}
}

func (bb *Buildbot) startMaster() error {
	bb.cleanupTwistdPidFile("master")
	startCmd := exec.Command(bb.BinPath, "start", "master")
	startCmd.Dir = bb.WorkDir
	startOutput, err := startCmd.CombinedOutput()
	log.Printf("%s\n%s", startCmd, startOutput)
	if err != nil {
		return fmt.Errorf("buildbot start master: %v", err)
	}
	return nil
}

func (bb *Buildbot) startWorkers() error {
	for i, w := range bb.workers {
		bb.cleanupTwistdPidFile(w.name)
		startCmd := exec.Command(bb.WorkerBin, "start", w.name)
		startCmd.Dir = bb.WorkDir
		startOutput, err := startCmd.CombinedOutput()
		log.Printf("%s\n%s", startCmd, startOutput)
		if err != nil {
			for j := i - 1; j >= 0; j-- {
				err := bb.stopWorker(bb.workers[j].name)
				if err != nil {
					log.Printf("%v", err)
				}
			}
			return fmt.Errorf("buildbot-worker start %s: %v", w.name, err)
		}
	}
	return nil
}

func (bb *Buildbot) RewriteConfig(gps []*gerrit.Project) error {
	var b strings.Builder
	bb.writeConfig(&b, gps)
	path := filepath.Join(bb.WorkDir, "master", "master.cfg")
	err := os.WriteFile(path, []byte(b.String()), 0600)
	if err != nil {
		return fmt.Errorf("os.WriteFile('%s'): %v", path, err)
	}
	checkCmd := exec.Command(bb.BinPath, "checkconfig", "master")
	checkCmd.Dir = bb.WorkDir
	checkOutput, err := checkCmd.CombinedOutput()
	log.Printf("%s\n%s", checkCmd, checkOutput)
	if err != nil {
		return fmt.Errorf("buildbot checkconfig master: %v", err)
	}
	return nil
}

func (bb *Buildbot) Restart() error {
	restartCmd := exec.Command(bb.BinPath, "restart", "master")
	restartCmd.Dir = bb.WorkDir
	restartOutput, err := restartCmd.CombinedOutput()
	log.Printf("%s\n%s", restartCmd, restartOutput)
	if err != nil {
		return fmt.Errorf("buildbot restart master: %v", err)
	}
	return nil
}

func (bb *Buildbot) Stop() error {
	e1 := bb.stopMaster()
	e2 := bb.stopWorkers()
	if e1 != nil {
		if e2 != nil {
			log.Printf("%v", e2)
		}
		return e1
	}
	return e2
}

func (bb *Buildbot) stopMaster() error {
	stopCmd := exec.Command(bb.BinPath, "stop", "master")
	stopCmd.Dir = bb.WorkDir
	stopOutput, err := stopCmd.CombinedOutput()
	log.Printf("%s\n%s", stopCmd, stopOutput)
	if err != nil {
		return fmt.Errorf("buildbot stop master: %v", err)
	}
	return nil
}

func (bb *Buildbot) stopWorkers() error {
	var err error
	for _, w := range bb.workers {
		e := bb.stopWorker(w.name)
		if e != nil {
			if err != nil {
				log.Printf("%v", err)
			}
			err = e
		}
	}
	return err
}

func (bb *Buildbot) stopWorker(name string) error {
	stopCmd := exec.Command(bb.WorkerBin, "stop", name)
	stopCmd.Dir = bb.WorkDir
	stopOutput, err := stopCmd.CombinedOutput()
	log.Printf("%s\n%s", stopCmd, stopOutput)
	if err != nil {
		return fmt.Errorf("buildbot-worker stop %s: %v", name, err)
	}
	return nil
}

func (bb *Buildbot) printWorkerNames(w io.Writer) {
	fmt.Fprint(w, "workernames=[")
	for i, worker := range bb.workers {
		if i == 0 {
			fmt.Fprintf(w, "'%s'", worker.name)
		} else {
			fmt.Fprintf(w, ", '%s'", worker.name)
		}
	}
	fmt.Fprint(w, "], ")
}

func (bb *Buildbot) GetBuildbotURL() string {
	return fmt.Sprintf("%s://%s:%d", bb.WWWProtocol, bb.WWWHost, bb.PublicPort)
}
