package main

// The lint rules' documentation and the patterns they match.

type ruleDoc struct {
	severity, description, fix string
}

// ruleNames lists the rules in the order of the TypeScript RULES object.
var ruleNames = []string{
	"root-user", "latest-tag", "unpinned-digest", "secret-in-env", "secret-in-arg", "secret-literal", "no-healthcheck",
	"apt-no-cleanup", "apt-no-recommends", "apt-upgrade", "apk-no-cache", "pip-no-cache", "add-for-copy", "add-remote",
	"curl-pipe-shell", "sudo", "workdir-relative", "multiple-cmd", "shell-form-cmd", "expose-ssh", "maintainer-deprecated",
	"copy-whole-context", "copy-chown-missing", "empty-dockerfile", "from-missing",
}

var rules = map[string]ruleDoc{
	"root-user":             {"warning", "The final stage runs as root: there is no USER instruction, or the last one selects root or uid 0.", "Create an unprivileged user in the image and add USER <name> after the last instruction that needs root."},
	"latest-tag":            {"warning", "A base image is used without a tag or with :latest, so the build is not reproducible and can silently change.", "Pin a specific tag such as node:22-bookworm-slim."},
	"unpinned-digest":       {"info", "A base image is pinned by tag only; tags can be re-pointed, a digest cannot.", "Add the digest: FROM node:22-bookworm-slim@sha256:<digest>, and update it with your dependency tooling."},
	"secret-in-env":         {"error", "An ENV sets a variable whose name suggests a credential; the value is stored in the image configuration and every layer that reads it.", "Pass secrets at run time (environment from the orchestrator or a secrets manager) and never through ENV."},
	"secret-in-arg":         {"warning", "An ARG named like a credential; build arguments are recorded in the image history (docker history) even without a default.", "Use RUN --mount=type=secret,id=<name> and read the secret from /run/secrets/<name> during the build."},
	"secret-literal":        {"error", "A line contains what looks like a credential literal (private key header, cloud access key id, token prefix, password flag).", "Remove the literal, rotate the credential, and use a build secret mount."},
	"no-healthcheck":        {"info", "The final stage has no HEALTHCHECK, so the runtime cannot tell a hung container from a healthy one.", "Add HEALTHCHECK --interval=30s --timeout=3s CMD <probe> || exit 1, or HEALTHCHECK NONE deliberately for a base image."},
	"apt-no-cleanup":        {"warning", "apt-get install runs without removing /var/lib/apt/lists in the same RUN, so the package index is baked into the layer.", "Append && rm -rf /var/lib/apt/lists/* to the same RUN instruction."},
	"apt-no-recommends":     {"info", "apt-get install without --no-install-recommends pulls in packages that are rarely needed in a container.", "Add --no-install-recommends to apt-get install."},
	"apt-upgrade":           {"info", "apt-get upgrade or dist-upgrade in a Dockerfile makes the result depend on the day it was built.", "Use an up-to-date base image tag instead of upgrading at build time."},
	"apk-no-cache":          {"info", "apk add without --no-cache leaves the package index in the layer.", "Use apk add --no-cache <packages>."},
	"pip-no-cache":          {"info", "pip install without --no-cache-dir keeps downloaded wheels in the layer.", "Use pip install --no-cache-dir, or a cache mount."},
	"add-for-copy":          {"warning", "ADD is used for a plain local file or directory. ADD also extracts archives and fetches URLs, which is surprising; COPY does only what it says.", "Use COPY for local files. Keep ADD only for extracting a local tar archive."},
	"add-remote":            {"warning", "ADD fetches a remote URL at build time with no checksum, so the image depends on the remote content at build time.", "Download with curl or wget in a RUN, verify the checksum, and remove the downloader's cache; or vendor the file."},
	"curl-pipe-shell":       {"error", "A RUN pipes downloaded content straight into a shell, executing whatever the server (or an attacker in the path) returns.", "Download to a file, verify a checksum or signature, then run it."},
	"sudo":                  {"warning", "RUN uses sudo; the build already runs as root until USER, and sudo in images adds a setuid binary.", "Run the command directly before switching to a non-root user."},
	"workdir-relative":      {"warning", "WORKDIR is relative, so it depends on the previous WORKDIR and on the base image.", "Use an absolute path such as WORKDIR /app."},
	"multiple-cmd":          {"warning", "More than one CMD (or ENTRYPOINT) in a stage; only the last one takes effect.", "Keep one CMD and one ENTRYPOINT per stage."},
	"shell-form-cmd":        {"info", "CMD or ENTRYPOINT uses shell form, so the process runs under /bin/sh -c and does not receive SIGTERM directly.", `Use exec (JSON array) form: CMD ["node", "server.js"].`},
	"expose-ssh":            {"warning", "EXPOSE 22 suggests an SSH daemon in the container.", "Remove SSH from the image and use docker exec or the orchestrator's exec for debugging."},
	"maintainer-deprecated": {"info", "MAINTAINER is deprecated.", `Use LABEL org.opencontainers.image.authors="...".`},
	"copy-whole-context":    {"info", "COPY copies the entire build context; without a .dockerignore this includes .git, node_modules, .env files and other secrets.", "Copy only what the image needs, and add a .dockerignore that excludes .git, .env*, node_modules and build output."},
	"copy-chown-missing":    {"info", "Files are copied as root after USER switched to a non-root user, so the application cannot write to them.", "Use COPY --chown=<user>:<group> when the application needs write access, or copy before switching USER if read-only is intended."},
	"empty-dockerfile":      {"error", "No instructions were found.", "A Dockerfile starts with FROM (or ARG followed by FROM)."},
	"from-missing":          {"error", "The first non-ARG instruction is not FROM.", "Start with FROM <image>."},
}

var (
	reSecretName    = jsRegexp(`(PASS(WORD|WD)?|SECRET|TOKEN|API[_-]?KEY|PRIVATE[_-]?KEY|ACCESS[_-]?KEY|CREDENTIALS?|AUTH[_-]?KEY|CLIENT[_-]?SECRET)`, "i")
	reSecretLiteral = jsRegexp(`(-----BEGIN [A-Z ]*PRIVATE KEY-----|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9]{20,}|--password[= ]\S+|-p\s*['"]?[^\s'"$]{6,})`, "")
	reArchive       = jsRegexp(`\.(tar|tar\.gz|tgz|tar\.bz2|tbz2|tar\.xz|txz)$`, "i")
	reArgFlag       = jsRegexp(`--\S+(=\S+)?\s+`, "g")
	reEnvPair       = jsRegexp(`([A-Za-z_][\w.-]*)(?:=("(?:[^"\\]|\\.)*"|'[^']*'|\S*))?`, "g")
	reDigits        = jsRegexp(`^\d+$`, "")
	reArgReference  = jsRegexp(`^\$\{?([A-Za-z_]\w*).*$`, "")
	reLeadingQuote  = jsRegexp(`^["']`, "")
	reCurlPipe      = jsRegexp(`\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|da|k)?sh\b`, "")
	reShellProcSub  = jsRegexp(`\b(ba|z)?sh\s+<\(\s*(curl|wget)`, "")
	reSudo          = jsRegexp(`(^|[\s;&|])sudo\s`, "")
	reAptInstall    = jsRegexp(`\bapt(-get)?\s+(-\S+\s+)*install\b`, "")
	reAptListsRm    = jsRegexp(`rm\s+(-\S+\s+)*\/var\/lib\/apt\/lists`, "")
	reAptCacheMount = jsRegexp(`--mount=type=cache,[^ ]*\/var\/lib\/apt`, "")
	reAptUpgrade    = jsRegexp(`\bapt(-get)?\s+(-\S+\s+)*(dist-)?upgrade\b`, "")
	reApkAdd        = jsRegexp(`\bapk\s+(-\S+\s+)*add\b`, "")
	rePipInstall    = jsRegexp(`\bpip3?\s+(-\S+\s+)*install\b`, "")
	reRemoteSource  = jsRegexp(`^https?:\/\/`, "i")
	reGitSource     = jsRegexp(`^git@`, "")
	reAbsoluteDir   = jsRegexp(`^(\/|[A-Za-z]:\\|\$)`, "")
	reExposeSSH     = jsRegexp(`(^|\s)22(\/tcp)?(\s|$)`, "")
	reRootUser      = jsRegexp(`^(root|0)(:|$)`, "")
)
