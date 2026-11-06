package domain

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	idFolder = regexp.MustCompile(`/folders/([\w-]{10,})`)
	idDoc    = regexp.MustCompile(`/d/([\w-]{10,})`)
	idQuery  = regexp.MustCompile(`[?&]id=([\w-]{10,})`)
	idBare   = regexp.MustCompile(`^[\w-]{10,}$`)
)

// ParseDriveID pulls a file/folder id out of a Drive or Docs link.
func ParseDriveID(input string) string {
	s := strings.TrimSpace(input)
	for _, re := range []*regexp.Regexp{idFolder, idDoc, idQuery} {
		if m := re.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	if idBare.MatchString(s) {
		return s
	}
	return ""
}

func DocURL(docID, tabID string) string {
	u := "https://docs.google.com/document/d/" + docID + "/edit"
	if tabID != "" {
		u += "?tab=" + url.QueryEscape(tabID)
	}
	return u
}
