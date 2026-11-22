# syntax=docker/dockerfile:1
#
# pi-worker recipe — the image half of the v3 kit (see ./pi-worker.yaml).
#
# Shape B, "idiomatic": the whole toolchain and the agent's configuration are
# image content, not per-sandbox setup. That matters for the template flow,
# because `sbx template save` snapshots a *filesystem*, not a kit's runtime
# hooks:
#   * ENV here is IMAGE environment, so it survives the snapshot. (In v2 these
#     were `environment.variables`, applied per sandbox at create time and lost
#     by `sbx template save` — a sandbox created from the bare template reported
#     Go's defaults.)
#   * RUN installs happen once, at build time. No install hook runs at create
#     time, so nothing at runtime needs npm/go.dev egress: the network policy's
#     `install.allow` list in the descriptor exists only for this build.
#   * COPY bakes the agent config into the image. The difference from v2's
#     `setup.files`: a COPY'd file is NOT rewritten on every boot, so edits a
#     worker makes to ~/.pi/agent/*.json persist for that sandbox's lifetime
#     instead of being reset at create time.
FROM docker/sandbox-templates:shell-docker

# Version pins, in one place. build.sh asserts GO_VERSION against the repo's
# go.mod `go` directive and PI_VERSION against the `pi` pin in this recipe.
ARG GO_VERSION=1.27.1
ARG PI_VERSION=1.6.0

# Image-level environment (survives `sbx template save`, unlike v2 kit env):
#   GOTOOLCHAIN  never download a different toolchain than the pinned one
#   GOPROXY      module proxy ONLY — no `,direct`, so module bytes can only come
#                from proxy.golang.org; a proxy outage fails loudly instead of
#                falling back to direct VCS fetching
#   GOSUMDB      one checksum database
#   PI_AGENT_HOME  where the in-VM pi reads settings/models/auth from
# GOCACHE/GOMODCACHE are deliberately NOT set: they stay at the image defaults
# (/home/agent/.cache/go-build, /home/agent/go/pkg/mod) so the module cache that
# build.sh warms lands inside the filesystem snapshot.
ENV GOTOOLCHAIN=local \
    GOPROXY=https://proxy.golang.org \
    GOSUMDB=sum.golang.org \
    PI_AGENT_HOME=/home/agent/.pi/agent

# The base image runs as `agent`; system-level installs need root, the agent's
# own files are written after switching back (same pattern as the official
# custom-template example for docker/sandbox-templates).
USER root

# Go from the official tarball, arch-detected. Same version, same layout as the
# v2 install hook: /usr/local/go + symlinks in /usr/local/bin. curl is the fast
# path; wget and node are fallbacks, since any one of them may be missing from a
# future base image.
RUN set -eu; \
    case "$(uname -m)" in \
      aarch64|arm64) GOARCH=arm64 ;; \
      x86_64|amd64) GOARCH=amd64 ;; \
      *) echo "pi-worker: unsupported machine type $(uname -m)" >&2; exit 1 ;; \
    esac; \
    url="https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz"; \
    echo "pi-worker: installing Go ${GO_VERSION} (${GOARCH}) from ${url}"; \
    tmp="$(mktemp -d)"; \
    if command -v curl >/dev/null 2>&1; then \
      curl -fsSL "$url" -o "$tmp/go.tgz"; \
    elif command -v wget >/dev/null 2>&1; then \
      wget -q "$url" -O "$tmp/go.tgz"; \
    else \
      node -e 'const [u,f]=process.argv.slice(1);fetch(u).then(r=>{if(!r.ok)throw new Error("HTTP "+r.status);return r.arrayBuffer()}).then(b=>require("node:fs").writeFileSync(f,Buffer.from(b))).catch(e=>{console.error(e);process.exit(1)})' "$url" "$tmp/go.tgz"; \
    fi; \
    rm -rf /usr/local/go; \
    tar -C /usr/local -xzf "$tmp/go.tgz"; \
    rm -rf "$tmp"; \
    ln -sf /usr/local/go/bin/go /usr/local/bin/go; \
    ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt; \
    /usr/local/go/bin/go version

# pi, pinned. `--ignore-scripts` because the package has no install step we want
# to run at image-build time; the cache is dropped so the image does not carry
# the npm tarball cache into every snapshot.
RUN set -eu; \
    echo "pi-worker: installing @earendil-works/pi-coding-agent@${PI_VERSION} globally"; \
    npm install -g --ignore-scripts "@earendil-works/pi-coding-agent@${PI_VERSION}"; \
    npm cache clean --force >/dev/null 2>&1 || true; \
    pi --version

# pi EXTENSION packages, baked. The mountless researcher is the only role whose
# tools live outside the pi core binary: `web_search`/`fetch_content`/
# `source_check`/`get_search_content` come from `pi-web-access`, and
# `resolve-library-id`/`query-docs` from `@upstash/context7-pi`. Without them the
# sandboxed researcher starts with no web tools at all — the host only has them
# because its own settings.json declares the same two npm packages.
#
# pi's own layout is reproduced exactly: `pi install npm:<pkg>` writes into
# `<agent dir>/npm/` (`PI_AGENT_HOME=/home/agent/.pi/agent`) using an
# npm project whose package.json is `{"name":"pi-extensions","private":true}`
# plus a `dependencies` map, and records the declaration in settings.json (the
# COPY below carries it). A pre-populated `node_modules` is what makes startup
# offline-safe: pi only reinstalls a package whose installed version fails the
# configured range, and an UNVERSIONED settings entry (`npm:pi-web-access`)
# accepts any installed version, so a baked install is simply loaded.
#
# EXACT pins, no carets: the image — not the registry — decides the version, so
# a rebuild is reproducible and this is the only place a version can move.
# `--legacy-peer-deps --no-audit --no-fund` mirrors pi's own install argv (pi
# supplies `@earendil-works/pi-*` as loader aliases, so resolving peers would
# pull a physical duplicate of the host's API packages).
#
# Root here, then `chown -R 1000:1000`: the agent must own its own config tree,
# and npm's cache is dropped so the image does not carry it into every snapshot.
RUN set -eu; \
    echo "pi-worker: baking pi extensions (pi-web-access, @upstash/context7-pi)"; \
    mkdir -p /home/agent/.pi/agent/npm; \
    printf '%s\n' \
      '{' \
      '  "name": "pi-extensions",' \
      '  "private": true,' \
      '  "dependencies": {' \
      '    "@upstash/context7-pi": "0.1.3",' \
      '    "pi-web-access": "0.48.0"' \
      '  }' \
      '}' > /home/agent/.pi/agent/npm/package.json; \
    HOME=/home/agent npm install --prefix /home/agent/.pi/agent/npm \
      --legacy-peer-deps --no-audit --no-fund; \
    rm -rf /home/agent/.npm; \
    chown -R 1000:1000 /home/agent/.pi; \
    test -d /home/agent/.pi/agent/npm/node_modules/pi-web-access; \
    test -d /home/agent/.pi/agent/npm/node_modules/@upstash/context7-pi; \
    ls -l /home/agent/.pi/agent/npm/package.json

USER agent

# Write the same values into the agent's own `go env` file (/home/agent/
# .config/go/env), so `go env GOPROXY GOTOOLCHAIN GOSUMDB` agrees with the image
# ENV even in a context that drops the container's ENV. Also paves the config
# directory the COPY below fills. HOME/GOENV are explicit: as a non-root USER,
# a build context without HOME must not decide where the go env file lands.
RUN set -eu; \
    mkdir -p /home/agent/.pi/agent /home/agent/.config/go; \
    HOME=/home/agent GOENV=/home/agent/.config/go/env \
      go env -w GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOTOOLCHAIN=local; \
    HOME=/home/agent GOENV=/home/agent/.config/go/env \
      go env GOPROXY GOTOOLCHAIN GOSUMDB; \
    HOME=/home/agent pi --version

# The agent's pi configuration, baked into the image. No secret is stored here:
# models.json's apiKey is the `$OPENCODE_GO_API_KEY` sentinel, and the sbx host
# proxy substitutes the real key (or a proxy placeholder) on requests to
# opencode.ai only. settings.json's `packages` list is what makes pi LOAD the
# extension packages installed above; it names them WITHOUT a version, because
# the image pins the version in the npm project's package.json.
COPY --chown=1000:1000 files/models.json files/settings.json /home/agent/.pi/agent/

# Web search, baked for the MOUNTLESS researcher role (the only agent whose
# frontmatter grants web_search). `{"provider":"openai"}` makes pi call
# api.openai.com/v1, whose credential is the OPENAI_API_KEY sentinel — same
# custom-secret mechanism as the model key (the VM only ever holds the
# `sbx-cs-` placeholder; the host substitutes the real key in the header). A
# role without web tools never reads this file, so it changes nothing for them.
COPY --chown=1000:1000 files/web-search.json /home/agent/.pi/agent/web-search.json

# Baked-config assertion: the files are present, owned by the agent, and the
# default model the template will start with resolves in the catalog.
RUN set -eu; \
    ls -l /home/agent/.pi/agent/settings.json /home/agent/.pi/agent/models.json \
          /home/agent/.pi/agent/web-search.json; \
    grep -q 'glm-5.3' /home/agent/.pi/agent/settings.json; \
    grep -q 'OPENCODE_GO_API_KEY' /home/agent/.pi/agent/models.json; \
    grep -q '"provider":"openai"' /home/agent/.pi/agent/web-search.json; \
    grep -q 'npm:pi-web-access' /home/agent/.pi/agent/settings.json; \
    grep -q 'npm:@upstash/context7-pi' /home/agent/.pi/agent/settings.json; \
    ls -ld /home/agent/.pi/agent/npm/node_modules/pi-web-access \
           /home/agent/.pi/agent/npm/node_modules/@upstash/context7-pi; \
    test -f /home/agent/.pi/agent/npm/node_modules/pi-web-access/package.json; \
    test -f /home/agent/.pi/agent/npm/node_modules/@upstash/context7-pi/package.json; \
    echo "pi-worker: baked agent config + extension packages ok"
