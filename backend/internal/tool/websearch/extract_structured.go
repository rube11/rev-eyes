package websearch

// Read bounded Schema.org data supplied by the fetched page. No JavaScript,
// remote contexts or @id references are executed/fetched. These remain publisher
// claims, not independently verified schedules, prices or availability.

import (
	"encoding/json"
	"html"
	"io"
	"regexp"
	"strings"
)

const maxStructuredRecords = 24

var inactiveStructuredMarkup = regexp.MustCompile(`(?is)<(?:template|noscript|textarea|pre|code)[^>]*>.*?</(?:template|noscript|textarea|pre|code)\s*>`)
var structuredNumericPrice = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

func extractStructuredEvidence(markup string) string {
	markup = htmlCommentPattern.ReplaceAllString(markup, " ")
	markup = inactiveStructuredMarkup.ReplaceAllString(markup, " ")
	var records []string
	seen := make(map[string]bool)
	nodes := 0
	var walk func(any, string, int)
	appendRecord := func(parts []string) {
		line := strings.Join(parts, "; ")
		// Keep each entity and its qualifiers atomic through chunk selection.
		// Discard an oversized record instead of truncating a cancellation,
		// expiration or price qualifier off its end.
		if len([]rune(line)) > maxChunkLength || seen[line] || len(records) >= maxStructuredRecords {
			return
		}
		seen[line] = true
		records = append(records, line)
	}
	walk = func(value any, owner string, depth int) {
		if depth > 8 || nodes >= 256 || len(records) >= maxStructuredRecords {
			return
		}
		nodes++
		if array, ok := value.([]any); ok {
			for _, item := range array {
				walk(item, owner, depth+1)
			}
			return
		}
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		kind := structuredKind(object["@type"])
		name := structuredScalar(object["name"], 100)
		if kind != "" && name != "" {
			identity := "Page structured data: " + kind + " " + name
			base := []string{identity}
			invalidField := false
			if owner != "" && owner != name {
				base = append(base, "parent="+owner)
			}
			add := func(key string, value any, limit int) {
				if text := structuredScalar(value, limit); text != "" {
					base = append(base, key+"="+text)
				} else if value != nil {
					invalidField = true
				}
			}
			if strings.HasSuffix(kind, "Event") {
				// Status and both interval endpoints belong to THIS event only.
				add("eventStatus", object["eventStatus"], 80)
				add("startDate", object["startDate"], 40)
				add("endDate", object["endDate"], 40)
				add("previousStartDate", object["previousStartDate"], 40)
				if object["location"] != nil {
					add("location", structuredLocation(object["location"]), 120)
				}
			} else {
				if object["address"] != nil {
					add("address", structuredLocation(object["address"]), 120)
				}
				add("servesCuisine", object["servesCuisine"], 80)
				add("priceRange", object["priceRange"], 60)
			}
			if kind == "MenuItem" {
				// These fields describe this exact dish, not its restaurant or
				// siblings. Keep dietary and service qualifiers attached to price.
				add("description", object["description"], 300)
				add("suitableForDiet", object["suitableForDiet"], 120)
			}
			if free, ok := object["isAccessibleForFree"].(bool); ok {
				if free {
					base = append(base, "entry is free")
				} else {
					base = append(base, "entry is not free")
				}
			}
			if invalidField {
				return
			}
			offers := structuredObjects(object["offers"])
			if len(offers) == 0 {
				if len(base) > 1 {
					appendRecord(base)
				}
			} else {
				for index, offer := range offers {
					if index >= 4 {
						break
					}
					parts := append([]string(nil), base...)
					invalidOffer := false
					for _, key := range []string{"name", "price", "lowPrice", "highPrice", "priceCurrency", "availability", "validFrom", "validThrough", "priceValidUntil"} {
						if text := structuredScalar(offer[key], 80); text != "" {
							if (key == "price" || key == "lowPrice" || key == "highPrice") && structuredNumericPrice.MatchString(text) && structuredScalar(offer["priceCurrency"], 3) == "USD" {
								text = "$" + text // Currency is explicit on this same offer.
							}
							parts = append(parts, "offer."+key+"="+text)
						} else if offer[key] != nil {
							invalidOffer = true
						}
					}
					if !invalidOffer {
						appendRecord(parts)
					}
				}
			}
			if !strings.HasSuffix(kind, "Event") && kind != "MenuItem" {
				for _, property := range []string{"openingHoursSpecification", "specialOpeningHoursSpecification"} {
					for _, hours := range structuredObjects(object[property]) {
						parts := []string{identity, "venue " + property}
						invalidHours := false
						// A location's opening hours never become its events' hours.
						for _, key := range []string{"dayOfWeek", "opens", "closes", "validFrom", "validThrough"} {
							if text := structuredScalar(hours[key], 120); text != "" {
								parts = append(parts, key+"="+text)
							} else if hours[key] != nil {
								invalidHours = true
							}
						}
						if len(parts) > 2 && !invalidHours {
							appendRecord(parts)
						}
					}
				}
			}
		}
		// Deterministic, explicit entity containers only. Do not ingest arbitrary
		// review/rating prose or merge fields from a sibling/parent object.
		for _, key := range []string{"@graph", "mainEntity", "itemListElement", "item", "subEvent", "hasMenu", "hasMenuSection", "hasMenuItem"} {
			childOwner := owner
			if key != "@graph" && name != "" {
				childOwner = name
				if owner != "" && owner != name {
					childOwner = owner + " > " + name
				}
				if len([]rune(childOwner)) > 160 {
					continue // Do not detach a menu item's price from its owner.
				}
			}
			walk(object[key], childOwner, depth+1)
		}
	}
	ldBlocks := 0
	for _, match := range jsonLDElementPattern.FindAllStringSubmatch(markup, -1) {
		attributes := htmlAttributes(match[1])
		if !strings.EqualFold(strings.TrimSpace(attributes["type"]), "application/ld+json") {
			continue
		}
		if ldBlocks >= 16 {
			break
		}
		ldBlocks++
		if len(match[2]) > 128<<10 {
			continue
		}
		var value any
		decoder := json.NewDecoder(strings.NewReader(match[2]))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			continue
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			continue
		}
		walk(value, "", 0)
	}
	return strings.Join(records, "\n")
}

func structuredKind(value any) string {
	if array, ok := value.([]any); ok {
		for _, item := range array {
			if kind := structuredKind(item); kind != "" {
				return kind
			}
		}
		return ""
	}
	name, _ := value.(string)
	name = strings.TrimPrefix(strings.TrimPrefix(name, "https://schema.org/"), "http://schema.org/")
	switch name {
	case "Event", "MusicEvent", "ComedyEvent", "TheaterEvent", "ScreeningEvent", "DanceEvent", "SportsEvent", "FoodEvent", "Festival", "Restaurant", "CafeOrCoffeeShop", "BarOrPub", "LocalBusiness", "FoodEstablishment", "Place", "MenuItem":
		// Festival has Event fields despite its name lacking the suffix.
		if name == "Festival" {
			return "Event"
		}
		return name
	}
	return ""
}

func structuredScalar(value any, limit int) string {
	return structuredScalarDepth(value, limit, 0)
}

func structuredScalarDepth(value any, limit, depth int) string {
	if depth > 4 {
		return ""
	}
	var text string
	switch value := value.(type) {
	case string:
		text = value
	case json.Number:
		text = string(value)
	case []any:
		var parts []string
		for _, item := range value {
			if len(parts) >= 7 {
				return "" // Do not silently remove an eighth qualifier.
			}
			if part := structuredScalarDepth(item, limit, depth+1); part != "" {
				parts = append(parts, part)
			} else {
				return ""
			}
		}
		text = strings.Join(parts, ", ")
	}
	text = strings.Join(strings.Fields(html.UnescapeString(allTagPattern.ReplaceAllString(text, " "))), " ")
	if len([]rune(text)) > limit {
		return "" // Never truncate a qualifier into a different statement.
	}
	return text
}

func structuredObjects(value any) []map[string]any {
	if object, ok := value.(map[string]any); ok {
		return []map[string]any{object}
	}
	var objects []map[string]any
	if array, ok := value.([]any); ok {
		for _, item := range array {
			if object, ok := item.(map[string]any); ok {
				objects = append(objects, object)
			}
		}
	}
	return objects
}

func structuredLocation(value any) string {
	return structuredLocationDepth(value, 0)
}

func structuredLocationDepth(value any, depth int) string {
	if depth > 4 {
		return ""
	}
	object, ok := value.(map[string]any)
	if !ok {
		return structuredScalar(value, 120)
	}
	var parts []string
	for _, key := range []string{"name", "streetAddress", "addressLocality", "addressRegion", "postalCode"} {
		if text := structuredScalar(object[key], 80); text != "" {
			parts = append(parts, text)
		}
	}
	if address := structuredLocationDepth(object["address"], depth+1); address != "" {
		parts = append(parts, address)
	}
	return strings.Join(parts, ", ")
}
