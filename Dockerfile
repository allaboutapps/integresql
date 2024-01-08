### -----------------------
# --- Stage: development
# --- Purpose: Local development environment
# --- https://hub.docker.com/_/golang
# --- https://github.com/microsoft/vscode-remote-try-go/blob/master/.devcontainer/Dockerfile
### -----------------------
FROM golang:1.21.5-bullseye AS development

# Avoid warnings by switching to noninteractive
ENV DEBIAN_FRONTEND=noninteractive

# Our Makefile / env fully supports parallel job execution
ENV MAKEFLAGS "-j 8 --no-print-directory"

# postgresql-support: Add the official postgres repo to install the matching postgresql-client tools of your stack
# https://wiki.postgresql.org/wiki/Apt
# run lsb_release -c inside the container to pick the proper repository flavor
# e.g. stretch=>stretch-pgdg, buster=>buster-pgdg, bullseye=>bullseye-pgdg
RUN echo "deb http://apt.postgresql.org/pub/repos/apt/ bullseye-pgdg main" \
    | tee /etc/apt/sources.list.d/pgdg.list \
    && wget --quiet -O - https://www.postgresql.org/media/keys/ACCC4CF8.asc \
    | apt-key add -

# Install required system dependencies
RUN apt-get update \
    && apt-get install -y \
    #
    # Mandadory minimal linux packages
    # Installed at development stage and app stage
    # Do not forget to add mandadory linux packages to the final app Dockerfile stage below!
    #
    # -- START MANDADORY --
    ca-certificates \
    # --- END MANDADORY ---
    #
    # Development specific packages
    # Only installed at development stage and NOT available in the final Docker stage
    # based upon
    # https://github.com/microsoft/vscode-remote-try-go/blob/master/.devcontainer/Dockerfile
    # https://raw.githubusercontent.com/microsoft/vscode-dev-containers/master/script-library/common-debian.sh
    #
    # icu-devtools: https://stackoverflow.com/questions/58736399/how-to-get-vscode-liveshare-extension-working-when-running-inside-vscode-remote
    # graphviz: https://github.com/google/pprof#building-pprof
    # -- START DEVELOPMENT --
    apt-utils \
    dialog \
    openssh-client \
    less \
    iproute2 \
    procps \
    lsb-release \
    locales \
    sudo \
    bash-completion \
    bsdmainutils \
    graphviz \
    xz-utils \
    postgresql-client-12 \
    icu-devtools \
    tmux \
    rsync \
    # --- END DEVELOPMENT ---
    #
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# env/vscode support: LANG must be supported, requires installing the locale package first
# https://github.com/Microsoft/vscode/issues/58015
# https://stackoverflow.com/questions/28405902/how-to-set-the-locale-inside-a-debian-ubuntu-docker-container
RUN sed -i -e 's/# en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen && \
    dpkg-reconfigure --frontend=noninteractive locales && \
    update-locale LANG=en_US.UTF-8

ENV LANG en_US.UTF-8

# sql pgFormatter: Integrates with vscode-pgFormatter (we pin pgFormatter.pgFormatterPath for the extension to this version)
# requires perl to be installed
# https://github.com/bradymholt/vscode-pgFormatter/commits/master
# https://github.com/darold/pgFormatter/releases
RUN mkdir -p /tmp/pgFormatter \
    && cd /tmp/pgFormatter \
    && wget https://github.com/darold/pgFormatter/archive/v5.5.tar.gz \
    && tar xzf v5.5.tar.gz \
    && cd pgFormatter-5.5 \
    && perl Makefile.PL \
    && make && make install \
    && rm -rf /tmp/pgFormatter

# go gotestsum: (this package should NOT be installed via go get)
# https://github.com/gotestyourself/gotestsum/releases
RUN mkdir -p /tmp/gotestsum \
    && cd /tmp/gotestsum \
    && ARCH="$(arch | sed s/aarch64/arm64/ | sed s/x86_64/amd64/)" \
    && wget "https://github.com/gotestyourself/gotestsum/releases/download/v1.11.0/gotestsum_1.11.0_linux_${ARCH}.tar.gz" \
    && tar xzf "gotestsum_1.11.0_linux_${ARCH}.tar.gz" \
    && cp gotestsum /usr/local/bin/gotestsum \
    && rm -rf /tmp/gotestsum

# go linting: (this package should NOT be installed via go get)
# https://github.com/golangci/golangci-lint#binary
# https://github.com/golangci/golangci-lint/releases
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b $(go env GOPATH)/bin v1.55.2

# go swagger: (this package should NOT be installed via go get)
# https://github.com/go-swagger/go-swagger/releases
RUN ARCH="$(arch | sed s/aarch64/arm64/ | sed s/x86_64/amd64/)" \
    && curl -o /usr/local/bin/swagger -L'#' \
    "https://github.com/go-swagger/go-swagger/releases/download/v0.29.0/swagger_linux_${ARCH}" \
    && chmod +x /usr/local/bin/swagger

# lichen: go license util
# TODO: Install from static binary as soon as it becomes available.
# https://github.com/uw-labs/lichen/tags
RUN go install github.com/uw-labs/lichen@v0.1.7

# watchexec
# https://github.com/watchexec/watchexec/releases
RUN mkdir -p /tmp/watchexec \
    && cd /tmp/watchexec \
    && wget https://github.com/watchexec/watchexec/releases/download/v1.20.6/watchexec-1.20.6-$(arch)-unknown-linux-musl.tar.xz \
    && tar xf watchexec-1.20.6-$(arch)-unknown-linux-musl.tar.xz \
    && cp watchexec-1.20.6-$(arch)-unknown-linux-musl/watchexec /usr/local/bin/watchexec \
    && rm -rf /tmp/watchexec

# yq
# https://github.com/mikefarah/yq/releases
RUN mkdir -p /tmp/yq \
    && cd /tmp/yq \
    && ARCH="$(arch | sed s/aarch64/arm64/ | sed s/x86_64/amd64/)" \
    && wget "https://github.com/mikefarah/yq/releases/download/v4.30.5/yq_linux_${ARCH}.tar.gz" \
    && tar xzf "yq_linux_${ARCH}.tar.gz" \
    && cp "yq_linux_${ARCH}" /usr/local/bin/yq \
    && rm -rf /tmp/yq

# linux permissions / vscode support: Add user to avoid linux file permission issues
# Detail: Inside the container, any mounted files/folders will have the exact same permissions
# as outside the container - including the owner user ID (UID) and group ID (GID).
# Because of this, your container user will either need to have the same UID or be in a group with the same GID.
# The actual name of the user / group does not matter. The first user on a machine typically gets a UID of 1000,
# so most containers use this as the ID of the user to try to avoid this problem.
# 2020-04: docker-compose does not support passing id -u / id -g as part of its config, therefore we assume uid 1000
# https://code.visualstudio.com/docs/remote/containers-advanced#_adding-a-nonroot-user-to-your-dev-container
# https://code.visualstudio.com/docs/remote/containers-advanced#_creating-a-nonroot-user
ARG USERNAME=development
ARG USER_UID=1000
ARG USER_GID=$USER_UID

RUN groupadd --gid $USER_GID $USERNAME \
    && useradd -s /bin/bash --uid $USER_UID --gid $USER_GID -m $USERNAME \
    && echo $USERNAME ALL=\(root\) NOPASSWD:ALL > /etc/sudoers.d/$USERNAME \
    && chmod 0440 /etc/sudoers.d/$USERNAME

# vscode support: cached extensions install directory
# https://code.visualstudio.com/docs/remote/containers-advanced#_avoiding-extension-reinstalls-on-container-rebuild
RUN mkdir -p /home/$USERNAME/.vscode-server/extensions \
    /home/$USERNAME/.vscode-server-insiders/extensions \
    && chown -R $USERNAME \
    /home/$USERNAME/.vscode-server \
    /home/$USERNAME/.vscode-server-insiders

# linux permissions / vscode support: chown $GOPATH so $USERNAME can directly work with it
# Note that this should be the final step after installing all build deps
RUN mkdir -p /$GOPATH/pkg && chown -R $USERNAME /$GOPATH

# $GOBIN is where our own compiled binaries will live and other go.mod / VSCode binaries will be installed.
# It should always come AFTER our other $PATH segments and should be earliest targeted in stage "builder",
# as /app/bin will the shadowed by a volume mount via docker-compose!
# E.g. "which golangci-lint" should report "/go/bin" not "/app/bin" (where VSCode will place it).
# https://github.com/go-modules-by-example/index/blob/master/010_tools/README.md#walk-through
WORKDIR /app
ENV GOBIN /app/bin
ENV PATH $PATH:$GOBIN

### -----------------------
# --- Stage: builder
### -----------------------

FROM development as builder
WORKDIR /app
COPY Makefile /app/Makefile
COPY go.mod /app/go.mod
COPY go.sum /app/go.sum
COPY tools.go /app/tools.go
RUN make modules
COPY tools.go /app/tools.go
RUN make tools
COPY . /app/
RUN make go-build

### -----------------------
# --- Stage: integresql
### -----------------------

# Distroless images are minimal and lack shell access.
# https://github.com/GoogleContainerTools/distroless/blob/master/base/README.md
# The :debug image provides a busybox shell to enter.
# https://github.com/GoogleContainerTools/distroless#debug-images
FROM gcr.io/distroless/base-debian11:debug as integresql
COPY --from=builder /app/bin/integresql /
# Note that cmd is not supported with these kind of images, no shell included
# see https://github.com/GoogleContainerTools/distroless/issues/62
# and https://github.com/GoogleContainerTools/distroless#entrypoints
ENTRYPOINT [ "/integresql" ]