Layout of the workdir:

    ├── certs
    │   ├── nsbox.key
    │   └── nsbox.crt
    ├── keycloak
    │   ├── admin_password.txt
    │   ├── bin
    │   ├── client_secret.json
    │   ├── conf
    │   ├── data
    │   ├── lib
    │   ├── LICENSE.txt
    │   ├── providers
    │   ├── README.md
    │   ├── themes
    │   └── version.txt
    ├── mailpit
    │   └── mails.db
    ├── redmine
    │   ├── data
    │   │   ├── admin_api_key.txt
    │   │   ├── secret_token.rb
    │   │   ├── production.sqlite3
    │   │   └── version.txt
    │   ├── files
    │   └── log
    ├── gerrit
    │   ├── bin
    │   ├── cache
    │   ├── data
    │   ├── db
    │   ├── etc
    │   │   ├── ...
    │   │   ├── gerrit.config
    │   │   └── gerrit.config.bak
    │   ├── git
    │   ├── index
    │   ├── lib
    │   ├── logs
    │   ├── plugins
    │   ├── static
    │   ├── tmp
    │   └── version.txt
    ├── buildbot
    │   ├── sandbox
    │   ├── ssh
    │   │   ├── id_ed25519
    │   │   └── id_ed25519.pub
    │   ├── master
    │   ├── worker
    │   └── version.txt
    ├── httpd
    │   ├── conf.d
    │   │   └── x0auth_openidc.conf
    │   ├── logs
    │   │   ├── portal_access_log
    │   │   ├── portal_error_log
    │   │   ├── mailpit_access_log
    │   │   ├── mailpit_error_log
    │   │   ├── redmine_access_log
    │   │   ├── redmine_error_log
    │   │   ├── gerrit_access_log
    │   │   ├── gerrit_error_log
    │   │   ├── buildbot_access_log
    │   │   └── buildbot_error_log
    │   ├── metadata
    │   │   ├── nsbox.local%3A9992%2Frealms%2Fnsbox.provider
    │   │   └── nsbox.local%3A9992%2Frealms%2Fnsbox.client
    │   └── version.txt
