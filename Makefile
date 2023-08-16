all:
	$(MAKE) -C keycloak
	$(MAKE) -C redmine
	$(MAKE) -C portal
