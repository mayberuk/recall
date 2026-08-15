package repo

import "strings"

// normalizeRemote reduces a configured remote url to a host-and-path identity
// and the short repo name. The forms on this machine include ssh with a port,
// https, and scp-style git@host:path; folding them is what puts two checkouts
// cloned over different protocols under one identity. Case is folded for the
// same reason, which costs nothing until two repos differ only in case.
func normalizeRemote(raw string) (id, name string) {
	u := strings.TrimSpace(raw)
	u = strings.TrimRight(u, "/")
	if u == "" {
		return "", ""
	}

	var host, path string
	switch {
	case strings.Contains(u, "://"):
		_, rest, _ := strings.Cut(u, "://")
		host, path, _ = strings.Cut(rest, "/")
		host = stripUser(host)
		if colon := strings.LastIndex(host, ":"); colon >= 0 {
			host = host[:colon]
		}
	case isSCPStyle(u):
		host, path, _ = strings.Cut(u, ":")
		host = stripUser(host)
	default:
		path = u
	}

	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	path = strings.Trim(path, "/")
	if host == "" && path == "" {
		return "", ""
	}
	id = strings.ToLower(host)
	if id != "" && path != "" {
		id += "/"
	}
	id += strings.ToLower(path)

	name = id
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	return id, name
}

func isSCPStyle(u string) bool {
	colon := strings.Index(u, ":")
	return colon > 0 && !strings.Contains(u[:colon], "/")
}

func stripUser(host string) string {
	if at := strings.LastIndex(host, "@"); at >= 0 {
		return host[at+1:]
	}
	return host
}
