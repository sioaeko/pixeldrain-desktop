package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// updateURL is the GitHub API endpoint for the newest published release.
// No account data or API key is sent; tests point it at a local server.
var updateURL = "https://api.github.com/repos/sioaeko/pixeldrain-desktop/releases/latest"

// UpdateInfo tells the UI whether a newer release exists.
type UpdateInfo struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
}

// CheckUpdate asks GitHub for the latest release and compares versions.
func (a *App) CheckUpdate() (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return checkUpdate(ctx, appVersion)
}

func checkUpdate(ctx context.Context, current string) (UpdateInfo, error) {
	info := UpdateInfo{Current: current}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateURL, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "PixeldrainDesktop/"+current)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return info, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return info, fmt.Errorf(L("업데이트 확인 실패 (HTTP %d)", "Update check failed (HTTP %d)"), res.StatusCode)
	}
	var rel struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return info, err
	}
	info.Latest = strings.TrimPrefix(rel.TagName, "v")
	info.URL = rel.HTMLURL
	info.Available = !rel.Draft && !rel.Prerelease && newerVersion(info.Latest, current)
	return info, nil
}

// newerVersion reports whether a is a higher dotted version than b
// ("1.10.0" > "1.9.2"). Unparsable parts count as 0.
func newerVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
