package main

// Upstream test fixtures and the shapes of the tool results the tests decode.

const badDockerfile = `# syntax=docker/dockerfile:1
FROM node
MAINTAINER someone
ENV API_KEY=abc123 DEBUG=true
ARG DB_PASSWORD=hunter2
RUN apt-get update && apt-get install -y curl \
    && curl -sSL https://example.com/install.sh | bash
RUN sudo apk add git
RUN pip install requests
ADD ./src /app/src
ADD https://example.com/file.txt /tmp/file.txt
ADD dist.tar.gz /app/
WORKDIR app
COPY . .
EXPOSE 22 8080
CMD npm start
CMD ["npm", "run", "serve"]
`

const goodDockerfile = `FROM node:22-bookworm-slim@sha256:0000000000000000000000000000000000000000000000000000000000000000 AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY src ./src

FROM node:22-bookworm-slim@sha256:0000000000000000000000000000000000000000000000000000000000000000
RUN apt-get update && apt-get install -y --no-install-recommends tini \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build --chown=node:node /app /app
USER node
HEALTHCHECK --interval=30s CMD node healthcheck.js
ENTRYPOINT ["tini", "--"]
CMD ["node", "src/server.js"]
`

type reportedFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

type stageResult struct {
	Index int     `json:"index"`
	Name  *string `json:"name"`
	Base  string  `json:"base"`
	Line  int     `json:"line"`
}

type lintResult struct {
	Source       string            `json:"source"`
	Instructions int               `json:"instructions"`
	Stages       []stageResult     `json:"stages"`
	Findings     []reportedFinding `json:"findings"`
}

type instructionResult struct {
	Line        int    `json:"line"`
	EndLine     int    `json:"end_line"`
	Instruction string `json:"instruction"`
	Args        string `json:"args"`
	Stage       int    `json:"stage"`
}

type parseResult struct {
	Source       string              `json:"source"`
	Escape       string              `json:"escape"`
	Stages       []stageResult       `json:"stages"`
	Instructions []instructionResult `json:"instructions"`
}
