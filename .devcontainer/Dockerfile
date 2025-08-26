FROM mcr.microsoft.com/devcontainers/go:1-1.24-bullseye

RUN apt-get update && apt-get install -y \
    postgresql-client \
    make \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js and npm
RUN curl -fsSL https://deb.nodesource.com/setup_lts.x | bash - \
    && apt-get install -y nodejs

RUN cd /tmp && \
    go mod init temp && \
    go install github.com/air-verse/air@latest && \
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest && \
    go install github.com/go-delve/delve/cmd/dlv@latest && \
    go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest && \
    rm -rf /tmp/go.mod /tmp/go.sum

# Set up sudo access first
RUN usermod -aG sudo vscode && \
    echo "vscode ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# Switch to vscode user before installing Claude Code
USER vscode
WORKDIR /home/vscode

# Configure npm to use user directory for global packages
RUN mkdir -p ~/.npm-global && \
    npm config set prefix '~/.npm-global'

# Install Claude Code using npm
RUN npm install -g @anthropic-ai/claude-code

# Add npm global bin to PATH
RUN echo 'export PATH="$HOME/.npm-global/bin:$PATH"' >> ~/.bashrc && \
    echo 'export PATH="$HOME/.npm-global/bin:$PATH"' >> ~/.profile

WORKDIR /workspace