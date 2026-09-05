package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	pageBodyLimit           int64 = 2 << 20
	pageResponseHeaderLimit       = 64 << 10
	pageTimeout                   = 4 * time.Second
	extractionTotalTimeout        = 10 * time.Second
	extractionConcurrency         = 3
	maxExtractionRedirects        = 3
	maxChunksPerSource            = 3
	maxChunkLength                = 500
	maxEvidenceBlockLength        = 1000
	extractionNotRequested        = "not_requested"
	extractionUnavailable         = "unavailable"
	extractionSucceeded           = "succeeded"
)

var (
	discardElementPattern = regexp.MustCompile(`(?is)<(?:script|style|noscript|svg|template|nav|footer|select)[^>]*>.*?</(?:script|style|noscript|svg|template|nav|footer|select)\s*>`)
	blockTagPattern       = regexp.MustCompile(`(?i)</?(?:p|div|article|section|main|h[1-6]|li|br|tr|blockquote)[^>]*>`)
	allTagPattern         = regexp.MustCompile(`(?s)<[^>]+>`)
	spacePattern          = regexp.MustCompile(`[\t\r\f\v ]+`)
	newlinePattern        = regexp.MustCompile(`\n{3,}`)
	mainElementPattern    = regexp.MustCompile(`(?is)<main(?:\s[^>]*)?>(.*?)</main\s*>`)
	metaElementPattern    = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
	jsonLDElementPattern  = regexp.MustCompile(`(?is)<script\s+([^>]*)>(.*?)</script\s*>`)
	attributePattern      = regexp.MustCompile(`(?is)([a-z_:][a-z0-9_:.-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	htmlCommentPattern    = regexp.MustCompile(`(?s)<!--.*?-->`)
	inertElementPattern   = regexp.MustCompile(`(?is)<(?:script|style|noscript|template|textarea|pre|code)[^>]*>.*?</(?:script|style|noscript|template|textarea|pre|code)\s*>`)
	headingElementPattern = regexp.MustCompile(`(?is)<h([1-6])(?:\s[^>]*)?>(.*?)</h[1-6]\s*>`)
	headingLinePattern    = regexp.MustCompile(`^\[Heading ([1-6])\]\s*(.*)$`)
	styledLabelPattern    = regexp.MustCompile(`(?is)<(?:div|span)(\s[^>]*)?>([^<>]{2,120})</(?:div|span)\s*>`)
	strongLabelPattern    = regexp.MustCompile(`(?is)<p[^>]*>\s*<(?:b|strong)[^>]*>([^<>]{2,120})</(?:b|strong)>\s*</p>`)
	clockEvidencePattern  = regexp.MustCompile(`(?i)\b\d{1,2}(?::\d{2})?\s*(?:a\.?m\.?|p\.?m\.?)\b|(?:startDate|endDate)=\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`)
	priceEvidencePattern  = regexp.MustCompile(`(?:\$|€|£)\s*\d|offer\.(?:price|lowPrice|highPrice)=\d`)
	freeAdmissionPattern  = regexp.MustCompile(`(?i)\b(?:no[ -](?:entry[ -]|entrance[ -]|admission[ -])?fees?|(?:entry|entrance|admission|day[ -]use)\s+(?:is\s+)?free|free\s+(?:entry|entrance|admission|day[ -]use))\b`)
	tableRowPattern       = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr\s*>`)
	tableCellBoundary     = regexp.MustCompile(`(?is)</(?:td|th)\s*>\s*<(?:td|th)\b[^>]*>`)
	imageElementPattern   = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	tableCaptionPattern   = regexp.MustCompile(`(?is)<caption\b[^>]*>(.*?)</caption\s*>`)
	preElementPattern     = regexp.MustCompile(`(?is)<pre(?:\s[^>]*)?>(.*?)</pre\s*>`)
)

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type pageExtractor struct {
	client      *http.Client
	validateURL func(context.Context, *url.URL) error
}

func newPageExtractor() *pageExtractor {
	resolver := net.DefaultResolver
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            publicDialContext(resolver),
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           extractionConcurrency,
		MaxIdleConnsPerHost:    1,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    3 * time.Second,
		ResponseHeaderTimeout:  3 * time.Second,
		ExpectContinueTimeout:  500 * time.Millisecond,
		MaxResponseHeaderBytes: pageResponseHeaderLimit,
	}
	extractor := &pageExtractor{
		validateURL: func(ctx context.Context, target *url.URL) error {
			return validatePublicTarget(ctx, resolver, target)
		},
	}
	extractor.client = &http.Client{
		Transport: transport,
		Timeout:   pageTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxExtractionRedirects {
				return errors.New("page redirect limit exceeded")
			}
			return extractor.validateURL(request.Context(), request.URL)
		},
	}
	return extractor
}

func (e *pageExtractor) enrich(
	ctx context.Context,
	query string,
	results []Result,
	domains ...string,
) ([]Result, int) {
	if len(results) == 0 {
		return results, 0
	}
	extractionCtx, cancel := context.WithTimeout(ctx, extractionTotalTimeout)
	defer cancel()

	enriched := append([]Result(nil), results...)
	linked := make([][]evidenceLink, len(results))
	semaphore := make(chan struct{}, extractionConcurrency)
	var group sync.WaitGroup
	var lock sync.Mutex
	extracted := 0
	for index := range enriched {
		// New provenance describes this attempt, not stale evidence attached to
		// an earlier fetch. Keep the caller's input and slices untouched.
		priorExtractionStatus := enriched[index].ExtractionStatus
		enriched[index].PageExcerpts = nil
		enriched[index].PagePublishedDate = ""
		enriched[index].ExtractionStatus = extractionUnavailable
		enriched[index].DiscoverySnippet = truncate(enriched[index].DiscoverySnippet, maxSnippetLength)
		// Only legacy rows without provenance can use Snippet as discovery.
		// A prior fetched-only row must not relabel its page text on re-fetch.
		if priorExtractionStatus == "" && enriched[index].DiscoverySnippet == "" && enriched[index].DiscoveredFrom == "" {
			enriched[index].DiscoverySnippet = truncate(enriched[index].Snippet, maxSnippetLength)
		}
		// One channel for each source of evidence. Re-fetching must also clear
		// stale fetched text from the old combined snippet representation.
		enriched[index].Snippet = enriched[index].DiscoverySnippet
		group.Add(1)
		go func(index int) {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-extractionCtx.Done():
				return
			}

			source, err := url.Parse(enriched[index].URL)
			if err != nil {
				return
			}
			page, err := e.fetchPageRestricted(extractionCtx, enriched[index].URL, evidenceTargetAllowed(source, domains))
			if err != nil {
				return
			}
			linked[index] = discoverEvidenceLinks(query, page.sourceURL, page.markup)
			excerpt := selectQueryChunks(query, page.text)
			pageExcerpts := completePageExcerpts(excerpt, maxSnippetLength)
			if len(pageExcerpts) == 0 {
				return
			}
			enriched[index].PageExcerpts = pageExcerpts
			enriched[index].PagePublishedDate = page.publishedDate
			enriched[index].ExtractionStatus = extractionSucceeded
			lock.Lock()
			extracted++
			lock.Unlock()
		}(index)
	}
	group.Wait()
	// A separate one-hop budget retains all discovery results. Child evidence
	// is its own source, never spliced into the parent's snippet or date.
	children := e.fetchEvidenceLinks(extractionCtx, query, results, linked, domains)
	enriched = append(enriched, children...)
	extracted += len(children)
	return enriched, extracted
}

func (e *pageExtractor) fetch(ctx context.Context, rawURL string) (string, error) {
	page, err := e.fetchPage(ctx, rawURL)
	return page.text, err
}

type extractedPage struct {
	text          string
	title         string
	publishedDate string
	markup        string
	sourceURL     *url.URL
}

func (e *pageExtractor) fetchPage(ctx context.Context, rawURL string) (extractedPage, error) {
	return e.fetchPageRestricted(ctx, rawURL, nil)
}

func (e *pageExtractor) fetchPageRestricted(ctx context.Context, rawURL string, allowed func(*url.URL) bool) (extractedPage, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return extractedPage{}, errors.New("invalid page URL")
	}
	if err := e.validateURL(ctx, target); err != nil {
		return extractedPage{}, err
	}
	if allowed != nil && !allowed(target) {
		return extractedPage{}, errors.New("page URL is outside the evidence source scope")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return extractedPage{}, errors.New("create page request failed")
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", "RevEyesResearchBot/1.0")

	// Clone per request so concurrent extraction never mutates the shared
	// client. Validate every hop, including custom/test clients.
	client := *e.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= maxExtractionRedirects || (allowed != nil && !allowed(next.URL)) {
			return errors.New("page redirect is outside the evidence source scope or budget")
		}
		if err := e.validateURL(next.Context(), next.URL); err != nil {
			return err
		}
		if e.client.CheckRedirect != nil {
			return e.client.CheckRedirect(next, via)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return extractedPage{}, ctx.Err()
		}
		return extractedPage{}, errors.New("fetch page failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return extractedPage{}, errors.New("page returned unsuccessful status")
	}
	contentType := response.Header.Get("Content-Type")
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return extractedPage{}, errors.New("page content type is not HTML")
	}
	body, tooLarge, err := readBounded(response.Body, pageBodyLimit)
	if err != nil {
		return extractedPage{}, errors.New("read page failed")
	}
	if tooLarge {
		return extractedPage{}, errors.New("page exceeded size limit")
	}
	markup := string(body)
	text := extractReadableText(markup)
	if structured := extractStructuredEvidence(markup); structured != "" {
		text = strings.TrimSpace(text + "\n" + structured)
	}
	finalURL := target
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL
	}
	return extractedPage{text: text, title: extractPageTitle(markup), publishedDate: extractPublicationDate(markup), markup: markup, sourceURL: finalURL}, nil
}

// Publication metadata is evidence supplied by the page, not proof of when the
// described event happened. Do not substitute modification/footer/event dates.
func extractPublicationDate(markup string) string {
	markup = htmlCommentPattern.ReplaceAllString(markup, " ")
	metaMarkup := inertElementPattern.ReplaceAllString(markup, " ")
	for _, element := range metaElementPattern.FindAllString(metaMarkup, -1) {
		attributes := htmlAttributes(element)
		field := strings.ToLower(attributes["property"])
		if field == "" {
			field = strings.ToLower(attributes["name"])
		}
		if field == "" {
			field = strings.ToLower(attributes["itemprop"])
		}
		switch field {
		case "article:published_time", "og:published_time", "datepublished", "pubdate", "publishdate":
			if date := parsePublicationDate(attributes["content"]); date != "" {
				return date
			}
		}
	}
	dates := make(map[string]string)
	for _, match := range jsonLDElementPattern.FindAllStringSubmatch(markup, -1) {
		if !strings.EqualFold(htmlAttributes(match[1])["type"], "application/ld+json") {
			continue
		}
		var payload any
		if json.Unmarshal([]byte(match[2]), &payload) == nil {
			collectArticlePublicationDates(payload, 0, dates)
		}
	}
	if len(dates) == 1 {
		for _, date := range dates {
			return date
		}
	}
	return ""
}

func htmlAttributes(markup string) map[string]string {
	attributes := make(map[string]string)
	for _, match := range attributePattern.FindAllStringSubmatch(markup, -1) {
		for _, value := range match[2:] {
			if value != "" {
				attributes[strings.ToLower(match[1])] = html.UnescapeString(value)
				break
			}
		}
	}
	return attributes
}

func parsePublicationDate(value string) string {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02", time.RFC1123Z, time.RFC1123} {
		if date, err := time.Parse(layout, value); err == nil {
			if layout == "2006-01-02" {
				return date.Format(layout)
			}
			return date.Format(time.RFC3339)
		}
	}
	return ""
}

func collectArticlePublicationDates(value any, depth int, dates map[string]string) {
	if depth > 8 {
		return
	}
	switch object := value.(type) {
	case []any:
		for _, item := range object {
			collectArticlePublicationDates(item, depth+1, dates)
		}
	case map[string]any:
		types := []any{object["@type"]}
		if values, ok := object["@type"].([]any); ok {
			types = values
		}
		for _, kind := range types {
			name, _ := kind.(string)
			name = strings.TrimPrefix(strings.TrimPrefix(name, "https://schema.org/"), "http://schema.org/")
			if name == "Article" || name == "NewsArticle" || name == "BlogPosting" {
				published, _ := object["datePublished"].(string)
				if date := parsePublicationDate(published); date != "" {
					key := date
					if parsed, err := time.Parse(time.RFC3339, date); err == nil {
						key = parsed.UTC().Format(time.RFC3339)
					}
					dates[key] = date
				}
			}
		}
		// Follow only graph containers, not arbitrary related articles/comments.
		collectArticlePublicationDates(object["@graph"], depth+1, dates)
	}
}

func publicDialContext(resolver ipResolver) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid page network address")
		}
		addresses, err := resolvePublicIPs(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, address := range addresses {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		if lastErr != nil {
			return nil, errors.New("connect to page failed")
		}
		return nil, errors.New("page host had no usable addresses")
	}
}

func validatePublicTarget(ctx context.Context, resolver ipResolver, target *url.URL) error {
	if target == nil || target.Host == "" || target.User != nil ||
		(target.Scheme != "http" && target.Scheme != "https") {
		return errors.New("page URL must use http or https without credentials")
	}
	_, err := resolvePublicIPs(ctx, resolver, target.Hostname())
	return err
}

func resolvePublicIPs(ctx context.Context, resolver ipResolver, host string) ([]net.IP, error) {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return nil, errors.New("page host is empty")
	}
	if literal := net.ParseIP(host); literal != nil {
		if !isPublicIP(literal) {
			return nil, errors.New("page host resolved to a non-public address")
		}
		return []net.IP{literal}, nil
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, errors.New("resolve page host failed")
	}
	if len(addresses) == 0 {
		return nil, errors.New("page host had no addresses")
	}
	public := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			// Reject the entire hostname when DNS mixes public and private
			// addresses so a retry cannot turn into an SSRF bypass.
			return nil, errors.New("page host resolved to a non-public address")
		}
		public = append(public, address.IP)
	}
	return public, nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() ||
		ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Go's IsPrivate deliberately excludes shared address space. It is not a
	// safe extraction destination in a cloud environment.
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1]&0xc0 == 64 { // 100.64.0.0/10
			return false
		}
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) { // benchmark network
			return false
		}
	}
	return true
}
