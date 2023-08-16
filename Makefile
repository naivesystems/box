all:
	$(MAKE) -C keycloak
	$(MAKE) -C redmine
	$(MAKE) -C gerrit
	$(MAKE) -C portal
