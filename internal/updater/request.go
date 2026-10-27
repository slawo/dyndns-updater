package updater

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var placeholder = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)

func decodeValues(raw string) (map[string]string, error) {
	values := map[string]string{}
	if raw == "" {
		return values, nil
	}
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil {
		return nil, errors.New("expected a JSON object of string values")
	}
	return values, nil
}

func (c config) render(raw, ip string, escape func(string) string) (string, error) {
	missing := false
	result := placeholder.ReplaceAllStringFunc(raw, func(token string) string {
		name := token[1 : len(token)-1]
		if name == "ip" || name == "ipv4" {
			return escape(ip)
		}
		value, ok := c.parameters[name]
		if !ok || value == "" {
			missing = true
			return ""
		}
		return escape(value)
	})
	if missing {
		return "", errors.New("template contains a missing or empty DYNDNS_PARAMETERS value")
	}
	return result, nil
}

func (c config) renderURL(ip string) (string, error) {
	// Credentials and record identifiers may change paths/queries, never the destination host.
	start := strings.Index(c.update, "://")
	if start < 0 {
		return "", errors.New("DYNDNS_UPDATE_URL must be an HTTPS URL")
	}
	end := strings.IndexAny(c.update[start+3:], "/?#")
	if end < 0 {
		end = len(c.update)
	} else {
		end += start + 3
	}
	if strings.ContainsAny(c.update[:end], "{}") {
		return "", errors.New("URL placeholders are only allowed in the path and query")
	}
	path, query, hasQuery := strings.Cut(c.update, "?")
	path, err := c.render(path, ip, url.PathEscape)
	if err != nil {
		return "", err
	}
	query, err = c.render(query, ip, url.QueryEscape)
	if err != nil {
		return "", err
	}
	raw := path
	if hasQuery {
		raw += "?" + query
	}
	if strings.ContainsAny(raw, "{}") || !validURL(raw) {
		return "", errors.New("DYNDNS_UPDATE_URL must be HTTPS without embedded credentials, fragments, or invalid placeholders")
	}
	return raw, nil
}

func validHeader(name, value string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	for _, r := range value {
		if r == 127 || r < 32 && r != '\t' {
			return false
		}
	}
	switch http.CanonicalHeaderKey(name) {
	case "Host", "Content-Length", "Transfer-Encoding", "Connection":
		return false
	}
	return true
}

func (c config) renderRequest(ip string) (string, string, map[string]string, error) {
	raw, err := c.renderURL(ip)
	if err != nil {
		return "", "", nil, err
	}
	headers := map[string]string{}
	for name, value := range c.headers {
		value, err = c.render(value, ip, func(s string) string { return s })
		if err != nil {
			return "", "", nil, err
		}
		name = http.CanonicalHeaderKey(name)
		if !validHeader(name, value) {
			return "", "", nil, errors.New("invalid DYNDNS_UPDATE_HEADERS")
		}
		if _, exists := headers[name]; exists {
			return "", "", nil, errors.New("duplicate DYNDNS_UPDATE_HEADERS name")
		}
		headers[name] = value
	}
	if c.user != "" && headers["Authorization"] != "" {
		return "", "", nil, errors.New("configure Basic authentication or an Authorization header, not both")
	}
	escape := func(s string) string { return s }
	if c.body != "" {
		if headers["Content-Type"] == "" {
			headers["Content-Type"] = "application/json"
		}
		switch strings.Split(headers["Content-Type"], ";")[0] {
		case "application/json":
			escape = func(s string) string {
				q, _ := json.Marshal(s) // A string is always JSON-encodable.
				return string(q[1 : len(q)-1])
			}
		case "application/x-www-form-urlencoded":
			escape = url.QueryEscape
		default:
			return "", "", nil, errors.New("DYNDNS_UPDATE_BODY supports JSON or form-urlencoded content")
		}
	}
	body, err := c.render(c.body, ip, escape)
	if err != nil {
		return "", "", nil, err
	}
	if body != "" && strings.Split(headers["Content-Type"], ";")[0] == "application/json" && !json.Valid([]byte(body)) {
		return "", "", nil, errors.New("DYNDNS_UPDATE_BODY is not valid JSON after substitution")
	}
	return raw, body, headers, nil
}

func (c config) successPattern(ip string) (*regexp.Regexp, error) {
	pattern, err := c.render(c.success, ip, regexp.QuoteMeta)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return nil, errors.New("invalid DYNDNS_SUCCESS_REGEX")
	}
	return re, nil
}

func (c config) validateUpdate() error {
	if !strings.Contains(c.update+c.body, "{ip}") && !strings.Contains(c.update+c.body, "{ipv4}") {
		return errors.New("update URL or body must contain {ip} or {ipv4}")
	}
	if c.method == "" {
		c.method = http.MethodGet
	}
	switch c.method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return errors.New("DYNDNS_UPDATE_METHOD must be GET, POST, PUT, or PATCH")
	}
	if c.method == http.MethodGet && c.body != "" {
		return errors.New("GET updates cannot have a request body")
	}
	for key := range c.parameters {
		if key == "ip" || key == "ipv4" || !placeholder.MatchString("{"+key+"}") || placeholder.FindString("{"+key+"}") != "{"+key+"}" {
			return errors.New("invalid or reserved DYNDNS_PARAMETERS key")
		}
	}
	if _, _, _, err := c.renderRequest("1.1.1.1"); err != nil {
		return err
	}
	if c.success != "" {
		_, err := c.successPattern("1.1.1.1")
		return err
	}
	return nil
}

func (c config) confirm(body, ip string) bool {
	if c.success != "" {
		re, err := c.successPattern(ip)
		return err == nil && re.MatchString(body)
	}
	// DynDNS2 providers may omit the echoed IP or return one result per hostname.
	if body == "" {
		return false
	}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 || len(fields) > 2 || (fields[0] != "good" && fields[0] != "nochg") || len(fields) == 2 && fields[1] != ip {
			return false
		}
	}
	return true
}

func (c config) publish(ctx context.Context, client *http.Client, ip string) error {
	raw, body, headers, err := c.renderRequest(ip)
	if err != nil {
		return err
	}
	method := c.method
	if method == "" {
		method = http.MethodGet
	}
	response, err := request(ctx, client, method, raw, body, headers, c.user, c.password)
	if err != nil {
		return err
	}
	if !c.confirm(response, ip) {
		return errors.New("update response did not match the configured success rule")
	}
	return nil
}
