# Plugin folders, from index.toml.
PLUGINS := $(shell sed -n 's/^path = "\(.*\)"/\1/p' index.toml)

.PHONY: build check install test-ui

# Each binary goes in its plugin's folder, where command = ["./<folder>"] finds it.
build:
	$(if $(PLUGINS),,$(error no plugins found in index.toml; each entry needs a line like path = "folder"))
	for p in $(PLUGINS); do CGO_ENABLED=0 go build -trimpath -o $$p/$$p ./$$p || exit 1; done

# Runs the contract kit from the core version go.mod pins. A plugin's
# check-config.json, if present, is its instance config for the check. A config
# makes the kit ingest first, which a catalog can't do without a database, so
# the config is sent only when INNKEEPER_PLUGIN_DATABASE_URL is set.
# A plugin that declares uses is checked against the ai plugin, which talks
# to the kit's fake model server.
check: build
	for p in $(PLUGINS); do cfg=; [ -z "$$INNKEEPER_PLUGIN_DATABASE_URL" ] || cfg="$$(cat $$p/check-config.json 2>/dev/null)"; INNKEEPER_PLUGIN_CONFIG="$$cfg" INNKEEPER_PROVIDER_PLUGIN=ai INNKEEPER_PROVIDER_CONFIG="$$(cat ai/check-provider.json)" go tool innkeeper plugin check $$p || exit 1; done

# Unit tests for each plugin's browser-side modules; these need only node.
test-ui:
	node --test */uitest/*.test.mjs

# Copies each plugin's binary, manifest, icon, and ui folder into DEST, such as an image's plugin folder.
# build runs after the DEST check, so a missing DEST fails before building.
install:
	$(if $(DEST),,$(error DEST is required, such as make install DEST=/plugins))
	$(MAKE) build
	for p in $(PLUGINS); do \
		mkdir -p "$(DEST)/$$p" || exit 1; \
		cp $$p/$$p $$p/plugin.toml $$(ls $$p/icon.* 2>/dev/null) "$(DEST)/$$p/" || exit 1; \
		if [ -d $$p/ui ]; then cp -R $$p/ui "$(DEST)/$$p/" || exit 1; fi; \
	done
