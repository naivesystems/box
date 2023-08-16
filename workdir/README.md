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
    │   ├── master
    │   ├── worker
    │   └── version.txt
    ├── httpd
    │   ├── conf.d
    │   │   ├── x0auth_openidc.conf
    │   │   ├── y8080.conf
    │   │   ├── y8443.conf
    │   │   ├── z9441redmine.conf
    │   │   ├── z9442gerrit.conf
    │   │   └── z9443buildbot.conf
    │   ├── html
    │   │   ├── index.html
    │   │   └── discover.html
    │   ├── logs
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
