package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type linkKind string

const (
	linkFile linkKind = "file"
	linkList linkKind = "list"
	linkFS   linkKind = "fs"
)

type parsedLink struct {
	Kind linkKind
	IDs  []string // file ids or the list id
	Path string   // filesystem path including the bucket
	Raw  string
}

var (
	idPattern    = regexp.MustCompile(`^[A-Za-z0-9]{4,32}$`)
	bareIDRe     = regexp.MustCompile(`^[A-Za-z0-9]{8}$`) // ids pasted without a URL
	tokenSplitRe = regexp.MustCompile(`[\s<>"']+`)
)

func isPixeldrainHost(host, apiHost string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	if host == strings.ToLower(apiHost) {
		return true
	}
	for _, h := range []string{"pixeldrain.com", "pixeldrain.net", "pixeldra.in", "pixeldrain.dev"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// parseLinks extracts pixeldrain links (and bare file ids) from free text.
func parseLinks(text, apiHost string) (links []parsedLink, invalid []string) {
	seen := map[string]bool{}
	for _, tok := range tokenSplitRe.Split(text, -1) {
		tok = strings.Trim(tok, "()[]{},.;")
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		if l, ok := parseLink(tok, apiHost); ok {
			links = append(links, l)
		} else {
			invalid = append(invalid, tok)
		}
	}
	return links, invalid
}

func parseLink(tok, apiHost string) (parsedLink, bool) {
	if bareIDRe.MatchString(tok) {
		return parsedLink{Kind: linkFile, IDs: []string{tok}, Raw: tok}, true
	}
	raw := tok
	if !strings.Contains(tok, "://") {
		tok = "https://" + tok
	}
	u, err := url.Parse(tok)
	if err != nil || !isPixeldrainHost(u.Hostname(), apiHost) {
		return parsedLink{}, false
	}
	p := strings.TrimPrefix(u.Path, "/api")
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if len(segs) < 2 {
		return parsedLink{}, false
	}
	switch segs[0] {
	case "u", "file":
		var ids []string
		for _, id := range strings.Split(segs[1], ",") {
			if idPattern.MatchString(id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return parsedLink{}, false
		}
		return parsedLink{Kind: linkFile, IDs: ids, Raw: raw}, true
	case "l", "list":
		if !idPattern.MatchString(segs[1]) {
			return parsedLink{}, false
		}
		return parsedLink{Kind: linkList, IDs: []string{segs[1]}, Raw: raw}, true
	case "d", "filesystem":
		return parsedLink{Kind: linkFS, Path: cleanFSPath(strings.Join(segs[1:], "/")), Raw: raw}, true
	}
	return parsedLink{}, false
}

// LinkResult tells the link dialog what was queued and what failed.
type LinkResult struct {
	Files   int      `json:"files"`
	Bytes   int64    `json:"bytes"`
	Errors  []string `json:"errors"`
	Invalid []string `json:"invalid"`
}

const maxWalkFiles = 100000

func (a *App) resolveLinks(ctx context.Context, text, dir string) (LinkResult, []downloadSource) {
	links, invalid := parseLinks(text, a.client.base.Hostname())
	res := LinkResult{Invalid: invalid}
	var srcs []downloadSource
	for _, l := range links {
		got, err := a.resolveLink(ctx, l, dir)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %s", l.Raw, err.Error()))
			continue
		}
		srcs = append(srcs, got...)
	}
	for _, s := range srcs {
		res.Files++
		res.Bytes += s.Size
	}
	return res, srcs
}

func (a *App) resolveLink(ctx context.Context, l parsedLink, dir string) ([]downloadSource, error) {
	switch l.Kind {
	case linkFile:
		files, err := a.client.FileInfo(ctx, l.IDs)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, errors.New(errorMessages["not_found"])
		}
		return fileSources(files, dir), nil
	case linkList:
		list, err := a.client.GetList(ctx, l.IDs[0])
		if err != nil {
			return nil, err
		}
		title := strings.TrimSpace(list.Title)
		if title == "" {
			title = list.ID
		}
		return fileSources(list.Files, filepath.Join(dir, sanitizeName(title))), nil
	default:
		return a.walkFS(ctx, l.Path, dir)
	}
}

func fileSources(files []pdFile, dir string) []downloadSource {
	out := make([]downloadSource, 0, len(files))
	for _, f := range files {
		out = append(out, downloadSource{Target: targetFiles, ID: f.ID, Name: f.Name, Size: f.Size, Hash: f.HashSHA256,
			LocalPath: filepath.Join(dir, sanitizeName(f.Name))})
	}
	return out
}

// walkFS expands a filesystem path into its files, recreating the folder
// structure under dir.
func (a *App) walkFS(ctx context.Context, p, dir string) ([]downloadSource, error) {
	st, err := a.client.FSStat(ctx, p)
	if err != nil {
		return nil, err
	}
	n := st.node()
	if n.Type == "file" {
		return []downloadSource{fsSource(p, n, filepath.Join(dir, sanitizeName(nodeName(n, p))))}, nil
	}
	var out []downloadSource
	var walk func(dirPath, local string, st *pdStat, depth int) error
	walk = func(dirPath, local string, st *pdStat, depth int) error {
		if depth > 64 {
			return errors.New("폴더가 너무 깊습니다")
		}
		for _, c := range st.Children {
			if err := ctx.Err(); err != nil {
				return err
			}
			cp := path.Join(dirPath, c.Name)
			if c.Type == "dir" {
				sub, err := a.client.FSStat(ctx, cp)
				if err != nil {
					return err
				}
				if err := walk(cp, filepath.Join(local, sanitizeName(c.Name)), sub, depth+1); err != nil {
					return err
				}
				continue
			}
			out = append(out, fsSource(cp, c, filepath.Join(local, sanitizeName(c.Name))))
			if len(out) > maxWalkFiles {
				return errors.New("파일이 너무 많습니다")
			}
		}
		return nil
	}
	if err := walk(cleanFSPath(p), filepath.Join(dir, sanitizeName(nodeName(n, p))), st, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func nodeName(n pdNode, p string) string {
	if n.Name != "" {
		return n.Name
	}
	return path.Base(cleanFSPath(p))
}

func fsSource(p string, n pdNode, local string) downloadSource {
	return downloadSource{Target: targetFS, RemotePath: cleanFSPath(p), Name: nodeName(n, p), Size: n.FileSize,
		Hash: n.SHA256, LocalPath: local}
}
