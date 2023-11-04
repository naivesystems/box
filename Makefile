export TAG ?= dev

all:
	@echo TAG=$(TAG)
	$(MAKE) -C keycloak
	$(MAKE) -C redmine
	$(MAKE) -C gerrit
	$(MAKE) -C httpd
	$(MAKE) -C buildbot
	$(MAKE) -C portal
