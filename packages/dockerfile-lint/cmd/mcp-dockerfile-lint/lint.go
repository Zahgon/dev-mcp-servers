package main

// The lint rules. All text here is in unit form (see jsstr.go).

import (
	"fmt"
	"sort"
	"strings"
)

type finding struct {
	rule     string
	severity string
	line     int
	message  string
}

func lint(p parsed, ignore map[string]bool) []finding {
	var findings []finding
	add := func(rule string, line int, message string) {
		if ignore[rule] {
			return
		}
		findings = append(findings, finding{rule: rule, severity: rules[rule].severity, line: line, message: message})
	}
	if len(p.instructions) == 0 {
		add("empty-dockerfile", 1, "no instructions found")
		return findings
	}
	for _, x := range p.instructions {
		if x.keyword != "ARG" {
			if x.keyword != "FROM" {
				add("from-missing", x.line, fmt.Sprintf("first instruction is %s, expected FROM", x.keyword))
			}
			break
		}
	}

	stageNames := map[string]bool{}
	for _, s := range p.stages {
		if s.name != nil {
			stageNames[*s.name] = true
		}
	}
	argNames := map[string]bool{}
	for _, x := range p.instructions {
		if x.keyword == "ARG" && x.stage == -1 {
			argNames[jsTrim(strings.Split(x.args, "=")[0])] = true
		}
	}
	for _, s := range p.stages {
		base := s.base
		if base == "scratch" || stageNames[base] || reDigits.MatchString(base) {
			continue
		}
		if strings.HasPrefix(base, "$") || strings.Contains(base, "${") {
			name := reArgReference.ReplaceAllString(base, "${1}")
			if !argNames[name] {
				add("latest-tag", s.line, fmt.Sprintf("FROM %s uses an undeclared build argument; the tag cannot be checked", base))
			}
			continue
		}
		hasDigest := strings.Contains(base, "@sha256:")
		withoutDigest := strings.Split(base, "@")[0]
		tagged := withoutDigest
		if k := strings.LastIndex(withoutDigest, "/"); k >= 0 {
			tagged = withoutDigest[k:]
		}
		parts := strings.Split(tagged, ":")
		hasTag := len(parts) > 1
		tag := ""
		if hasTag {
			tag = parts[1]
		}
		switch {
		case !hasDigest && (!hasTag || tag == "latest"):
			what := "has no tag"
			if tag == "latest" {
				what = "uses the latest tag"
			}
			add("latest-tag", s.line, fmt.Sprintf("FROM %s %s", base, what))
		case !hasDigest:
			add("unpinned-digest", s.line, fmt.Sprintf("FROM %s is pinned by tag only", base))
		}
	}

	for _, ins := range p.instructions {
		lintInstruction(ins, add)
	}

	for _, s := range p.stages {
		inStage := instructionsOf(p, s.index)
		for _, kind := range []string{"CMD", "ENTRYPOINT"} {
			var multiple []instruction
			for _, x := range inStage {
				if x.keyword == kind {
					multiple = append(multiple, x)
				}
			}
			if len(multiple) > 1 {
				label := fmt.Sprint(s.index)
				if s.name != nil {
					label = *s.name
				}
				add("multiple-cmd", multiple[len(multiple)-1].line, fmt.Sprintf("%s appears %d times in stage %s; only the last one applies", kind, len(multiple), label))
			}
		}
	}

	if len(p.stages) > 0 {
		lintFinalStage(p, add)
	}
	order := map[string]int{"error": 0, "warning": 1, "info": 2}
	sort.SliceStable(findings, func(a, b int) bool {
		x, y := findings[a], findings[b]
		if order[x.severity] != order[y.severity] {
			return order[x.severity] < order[y.severity]
		}
		return x.line < y.line
	})
	return findings
}

func instructionsOf(p parsed, stageIndex int) []instruction {
	var out []instruction
	for _, x := range p.instructions {
		if x.stage == stageIndex {
			out = append(out, x)
		}
	}
	return out
}

type addFunc func(rule string, line int, message string)

func lintInstruction(ins instruction, add addFunc) {
	a := ins.args
	switch ins.keyword {
	case "ENV":
		for _, pair := range envPairs(a) {
			if reSecretName.MatchString(pair.key) && pair.hasValue && pair.value != "" && !strings.HasPrefix(reLeadingQuote.ReplaceAllLiteralString(pair.value, ""), "$") {
				add("secret-in-env", ins.line, fmt.Sprintf("ENV %s looks like a credential with a literal value", pair.key))
			}
		}
	case "ARG":
		parts := strings.Split(a, "=")
		key := jsTrim(parts[0])
		if reSecretName.MatchString(key) {
			defaulted := ""
			if len(parts) > 1 {
				defaulted = " has a default value and"
			}
			add("secret-in-arg", ins.line, fmt.Sprintf("ARG %s%s is recorded in the image history", key, defaulted))
		}
	case "RUN":
		if reSecretLiteral.MatchString(a) {
			add("secret-literal", ins.line, "RUN contains what looks like a credential literal")
		}
		if reCurlPipe.MatchString(a) || reShellProcSub.MatchString(a) {
			add("curl-pipe-shell", ins.line, "downloaded content is piped into a shell")
		}
		if reSudo.MatchString(a) {
			add("sudo", ins.line, "RUN uses sudo")
		}
		if reAptInstall.MatchString(a) {
			if !reAptListsRm.MatchString(a) && !reAptCacheMount.MatchString(a) {
				add("apt-no-cleanup", ins.line, "apt-get install without rm -rf /var/lib/apt/lists/* in the same RUN")
			}
			if !strings.Contains(a, "--no-install-recommends") {
				add("apt-no-recommends", ins.line, "apt-get install without --no-install-recommends")
			}
		}
		if reAptUpgrade.MatchString(a) {
			add("apt-upgrade", ins.line, "apt-get upgrade at build time")
		}
		if reApkAdd.MatchString(a) && !strings.Contains(a, "--no-cache") && !strings.Contains(a, "--mount=type=cache") {
			add("apk-no-cache", ins.line, "apk add without --no-cache")
		}
		if rePipInstall.MatchString(a) && !strings.Contains(a, "--no-cache-dir") && !strings.Contains(a, "--mount=type=cache") {
			add("pip-no-cache", ins.line, "pip install without --no-cache-dir")
		}
	case "ADD":
		parts := splitArgs(a)
		if len(parts) > 0 {
			for _, src := range parts[:len(parts)-1] {
				if reRemoteSource.MatchString(src) || reGitSource.MatchString(src) {
					add("add-remote", ins.line, "ADD fetches "+src)
				} else if !reArchive.MatchString(src) {
					add("add-for-copy", ins.line, fmt.Sprintf("ADD %s copies a plain file or directory", src))
				}
			}
		}
	case "COPY":
		parts := splitArgs(a)
		if len(parts) >= 2 && (parts[0] == "." || parts[0] == "./" || parts[0] == "*") && !strings.Contains(a, "--from=") {
			add("copy-whole-context", ins.line, fmt.Sprintf("COPY %s %s copies the whole build context", parts[0], parts[len(parts)-1]))
		}
	case "WORKDIR":
		if !reAbsoluteDir.MatchString(a) && !strings.HasPrefix(a, "${") {
			add("workdir-relative", ins.line, fmt.Sprintf("WORKDIR %s is relative", a))
		}
	case "EXPOSE":
		if reExposeSSH.MatchString(a) {
			add("expose-ssh", ins.line, "EXPOSE 22")
		}
	case "MAINTAINER":
		add("maintainer-deprecated", ins.line, "MAINTAINER is deprecated")
	case "CMD", "ENTRYPOINT":
		if !strings.HasPrefix(a, "[") {
			add("shell-form-cmd", ins.line, ins.keyword+" uses shell form")
		}
	}
}

func lintFinalStage(p parsed, add addFunc) {
	last := p.stages[len(p.stages)-1]
	inStage := instructionsOf(p, last.index)
	named := ""
	if last.name != nil {
		named = " " + *last.name
	}
	var finalUser *instruction
	for k := range inStage {
		if inStage[k].keyword == "USER" {
			finalUser = &inStage[k]
		}
	}
	switch {
	case finalUser == nil:
		add("root-user", last.line, fmt.Sprintf("final stage%s has no USER instruction and runs as root", named))
	case reRootUser.MatchString(jsTrim(finalUser.args)):
		add("root-user", finalUser.line, "the last USER selects root")
	default:
		for _, c := range inStage {
			if (c.keyword == "COPY" || c.keyword == "ADD") && c.line > finalUser.line && !strings.Contains(c.args, "--chown=") {
				add("copy-chown-missing", c.line, fmt.Sprintf("%s after USER %s creates root-owned files", c.keyword, jsTrim(finalUser.args)))
			}
		}
	}
	hasHealthcheck := false
	for _, x := range inStage {
		if x.keyword == "HEALTHCHECK" {
			hasHealthcheck = true
		}
	}
	if !hasHealthcheck && last.base != "scratch" {
		add("no-healthcheck", last.line, fmt.Sprintf("final stage%s has no HEALTHCHECK", named))
	}
}
