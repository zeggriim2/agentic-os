# syntax=docker/dockerfile:1
#
# Image des agents d'exécution (Développeur, Reviewer, QA) du projet FraisZen.
#
# - Outillage de la CI FraisZen : PHP 8.5 (extensions de l'image FraisZen), Composer, Node 22.
# - Outillage des outils Hermes : git, python3, ripgrep.
# - Caches Composer et npm pré-remplis depuis les fichiers lock du projet : pendant les runs, le
#   conteneur n'a PAS de réseau (terminal.docker_network: false) ; `aos-setup` installe vendor/ et
#   node_modules/ depuis ces caches, sans rien télécharger.
#
# Construction (via `task project:image:build PROJECT=fraiszen`) :
#   docker build -f projects/fraiszen/Dockerfile --build-context project=<checkout develop> projects/fraiszen
# Le contexte nommé `project` ne sert qu'aux fichiers lock ; aucun secret n'entre dans l'image.

ARG PHP_IMAGE=dunglas/frankenphp:1-php8.5
ARG NODE_IMAGE=node:22-bookworm-slim

FROM ${NODE_IMAGE} AS node

FROM ${PHP_IMAGE}

SHELL ["/bin/bash", "-euo", "pipefail", "-c"]

# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates file git make openssl python3 ripgrep unzip \
 && install-php-extensions @composer apcu intl opcache pcntl sockets zip pdo_mysql \
 && rm -rf /var/lib/apt/lists/*

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
 && node --version && npm --version && php --version && composer --version

# Les agents tournent avec l'UID de l'hôte (pas d'entrée passwd, HOME=/ non inscriptible) : tout
# ce dont Composer, npm et Psalm ont besoin vit sous /opt/aos, inscriptible par tous (conteneur
# jetable). /tmp est monté noexec par Hermes : les copies de vérification vont dans
# /opt/aos/scratch (voir aos-ci).
ENV COMPOSER_HOME=/opt/aos/composer \
    COMPOSER_CACHE_DIR=/opt/aos/cache/composer \
    XDG_CACHE_HOME=/opt/aos/cache/xdg \
    AOS_SCRATCH_DIR=/opt/aos/scratch \
    npm_config_cache=/opt/aos/cache/npm \
    npm_config_update_notifier=false \
    COMPOSER_ALLOW_SUPERUSER=1

# Pré-remplissage des caches depuis les fichiers lock (avec réseau : uniquement pendant le build).
COPY --from=project composer.json composer.lock /tmp/warm/
COPY --from=project frontend/package.json frontend/package-lock.json /tmp/warm/frontend/
RUN cd /tmp/warm \
 && composer install --prefer-dist --no-progress --no-interaction --no-scripts --no-autoloader \
 && (cd frontend && npm ci --no-audit --no-fund --ignore-scripts) \
 && rm -rf /tmp/warm \
 && mkdir -p /opt/aos/composer /opt/aos/cache/xdg /opt/aos/scratch \
 && chmod -R a+rwX /opt/aos

# Pendant les runs : jamais de réseau, tout vient du cache.
ENV COMPOSER_DISABLE_NETWORK=1 \
    npm_config_offline=true \
    npm_config_audit=false \
    npm_config_fund=false

# QA navigateur (aos-qa-browser) : base MariaDB locale, Chromium headless piloté par Playwright,
# téléchargés au build uniquement. Les scénarios du QA importent playwright depuis /opt/aos/qa.
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/aos/ms-playwright
# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends mariadb-server curl \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /opt/aos/qa && cd /opt/aos/qa \
 && npm_config_offline=false npm install --no-audit --no-fund --no-save playwright@1.59.1 \
 && npm_config_offline=false npx playwright install --with-deps --only-shell chromium \
 && rm -rf /var/lib/apt/lists/* \
 && chmod -R a+rX /opt/aos/qa /opt/aos/ms-playwright
COPY --chmod=644 aos-qa-runner.mjs /opt/aos/qa/

COPY --chmod=755 aos-setup aos-ci aos-qa-browser /usr/local/bin/

# Hermes lance ses propres commandes : pas d'entrypoint FrankenPHP.
WORKDIR /tmp
ENTRYPOINT []
CMD ["bash"]

ARG AOS_LOCK_HASH=unknown
LABEL org.opencontainers.image.title="agentic-os agent image — fraiszen" \
      io.agentic-os.project="fraiszen" \
      io.agentic-os.lock-hash="${AOS_LOCK_HASH}"
