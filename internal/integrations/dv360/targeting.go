package dv360

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

const (
	targetingGeo            = "TARGETING_TYPE_GEO_REGION"
	targetingAge            = "TARGETING_TYPE_AGE_RANGE"
	targetingGender         = "TARGETING_TYPE_GENDER"
	targetingDevice         = "TARGETING_TYPE_DEVICE_TYPE"
	targetingTime           = "TARGETING_TYPE_DAY_AND_TIME"
	targetingFirstParty     = "TARGETING_TYPE_FIRST_PARTY_AND_PARTNER_AUDIENCE"
	targetingGoogleAudience = "TARGETING_TYPE_GOOGLE_AUDIENCE"
	targetingCombined       = "TARGETING_TYPE_COMBINED_AUDIENCE"
	targetingCustomList     = "TARGETING_TYPE_CUSTOM_LIST"
	targetingLookalike      = "TARGETING_TYPE_LOOKALIKE_AUDIENCE"
)

type assignedTargetingOption struct {
	TargetingType    string `json:"targetingType"`
	GeoRegionDetails *struct {
		TargetingOptionID string `json:"targetingOptionId"`
	} `json:"geoRegionDetails"`
	AgeRangeDetails *struct {
		AgeRange string `json:"ageRange"`
	} `json:"ageRangeDetails"`
	GenderDetails *struct {
		Gender string `json:"gender"`
	} `json:"genderDetails"`
	DeviceTypeDetails *struct {
		DeviceType string `json:"deviceType"`
	} `json:"deviceTypeDetails"`
	DayAndTimeDetails *struct {
		DayOfWeek string `json:"dayOfWeek"`
		StartHour int    `json:"startHour"`
		EndHour   int    `json:"endHour"`
	} `json:"dayAndTimeDetails"`
	FirstPartyDetails *struct {
		AudienceID string `json:"firstPartyAndPartnerAudienceId"`
	} `json:"firstPartyAndPartnerAudienceDetails"`
	GoogleAudienceDetails *struct {
		GoogleAudienceID   string `json:"googleAudienceId"`
		GoogleAudienceType string `json:"googleAudienceType"`
	} `json:"googleAudienceDetails"`
	CombinedAudienceDetails *struct {
		CombinedAudienceID string `json:"combinedAudienceId"`
	} `json:"combinedAudienceDetails"`
	CustomListDetails *struct {
		CustomListID   string `json:"customListId"`
		CustomListType string `json:"customListType"`
	} `json:"customListDetails"`
	LookalikeAudienceDetails *struct {
		LookalikeAudienceID string `json:"lookalikeAudienceId"`
	} `json:"lookalikeAudienceDetails"`
}

type targetingOption struct {
	TargetingOptionID string `json:"targetingOptionId"`
	GeoRegionDetails  *struct {
		GeoRegionType string `json:"geoRegionType"`
		DisplayName   string `json:"displayName"`
	} `json:"geoRegionDetails"`
}

// ensureTargeting applies the generic targeting spec to a line item. It is
// idempotent: existing assigned options are listed first and only missing ones
// are created, so resuming a launch never duplicates targeting.
func (c *Client) ensureTargeting(ctx context.Context, accessToken, advertiserID, lineItemID string, targeting map[string]any) error {
	if len(targeting) == 0 {
		return nil
	}

	existing, err := c.listAssignedTargeting(ctx, accessToken, advertiserID, lineItemID)
	if err != nil {
		return err
	}

	for _, opt := range c.targetingSpecs(ctx, accessToken, advertiserID, targeting) {
		if opt.err != nil {
			return opt.err
		}
		if opt.key == "" || existing[opt.key] {
			continue
		}
		if err := c.assignTargeting(ctx, accessToken, advertiserID, lineItemID, opt.targetingType, opt.details); err != nil {
			return err
		}
		existing[opt.key] = true
	}
	return nil
}

type targetingSpec struct {
	targetingType string
	key           string
	details       map[string]any
	err           error
}

func (c *Client) targetingSpecs(ctx context.Context, accessToken, advertiserID string, t map[string]any) []targetingSpec {
	var out []targetingSpec

	// Geo: geo or countries. Values may be country codes or place names.
	if geo, ok := stringSlice(t["geo"]); ok {
		out = append(out, c.geoSpecs(ctx, accessToken, advertiserID, geo)...)
	} else if countries, ok := stringSlice(t["countries"]); ok {
		out = append(out, c.geoSpecs(ctx, accessToken, advertiserID, countries)...)
	}

	// Age range buckets cover a continuous [age_min, age_max] interval.
	if minAge, ok := intValue(t["age_min"]); ok {
		maxAge := 65
		if v, ok := intValue(t["age_max"]); ok {
			maxAge = v
		}
		for _, b := range ageBuckets {
			if b.max < minAge || b.min > maxAge {
				continue
			}
			out = append(out, targetingSpec{
				targetingType: targetingAge,
				key:           "age:" + b.enum,
				details:       map[string]any{"ageRangeDetails": map[string]any{"ageRange": b.enum}},
			})
		}
	}

	// Gender: canonicalized to male/female; both means no constraint.
	if g, ok := t["genders"]; ok {
		for _, gender := range integrations.GenderValues(g) {
			enum := "GENDER_MALE"
			if gender == "female" {
				enum = "GENDER_FEMALE"
			}
			out = append(out, targetingSpec{
				targetingType: targetingGender,
				key:           "gender:" + enum,
				details:       map[string]any{"genderDetails": map[string]any{"gender": enum}},
			})
		}
	}

	// Devices: computer, mobile, tablet, tv, connected_device.
	deviceInput, ok := stringSlice(t["devices"])
	if !ok {
		deviceInput, ok = stringSlice(t["device"])
	}
	if ok {
		for _, d := range deviceInput {
			if enum := deviceType(d); enum != "" {
				out = append(out, targetingSpec{
					targetingType: targetingDevice,
					key:           "device:" + enum,
					details:       map[string]any{"deviceTypeDetails": map[string]any{"deviceType": enum}},
				})
			}
		}
	}

	// First-party / partner audiences (also accepted as "custom_audiences").
	for _, key := range []string{"first_party_audiences", "first_party_and_partner_audiences", "custom_audiences"} {
		if ids, ok := stringSlice(t[key]); ok {
			for _, id := range ids {
				out = append(out, targetingSpec{
					targetingType: targetingFirstParty,
					key:           "audience:fp:" + id,
					details: map[string]any{"firstPartyAndPartnerAudienceDetails": map[string]any{
						"firstPartyAndPartnerAudienceId": id,
						"negative":                       false,
					}},
				})
			}
			break
		}
	}

	// Google audiences: [{id, type}] or ["id"] (type defaults to AFFINITY).
	if entries, ok := audienceEntries(t["google_audiences"]); ok {
		for _, e := range entries {
			audienceType := googleAudienceType(e.audienceType)
			out = append(out, targetingSpec{
				targetingType: targetingGoogleAudience,
				key:           "audience:google:" + audienceType + ":" + e.id,
				details: map[string]any{"googleAudienceDetails": map[string]any{
					"googleAudienceId":   e.id,
					"googleAudienceType": audienceType,
					"negative":           false,
				}},
			})
		}
	}

	// Combined audiences (Google's prebuilt combinations).
	if ids, ok := stringSlice(t["combined_audiences"]); ok {
		for _, id := range ids {
			out = append(out, targetingSpec{
				targetingType: targetingCombined,
				key:           "audience:combined:" + id,
				details:       map[string]any{"combinedAudienceDetails": map[string]any{"combinedAudienceId": id}},
			})
		}
	}

	// Custom lists: [{id, type}] or ["id"] (type defaults to CUSTOM_AFFINITY).
	if entries, ok := audienceEntries(t["custom_lists"]); ok {
		for _, e := range entries {
			listType := customListType(e.audienceType)
			out = append(out, targetingSpec{
				targetingType: targetingCustomList,
				key:           "audience:customlist:" + listType + ":" + e.id,
				details: map[string]any{"customListDetails": map[string]any{
					"customListId":   e.id,
					"customListType": listType,
				}},
			})
		}
	}

	if ids, ok := stringSlice(t["lookalike_audiences"]); ok {
		for _, id := range ids {
			out = append(out, targetingSpec{
				targetingType: targetingLookalike,
				key:           "audience:lookalike:" + id,
				details: map[string]any{"lookalikeAudienceDetails": map[string]any{
					"lookalikeAudienceId": id,
					"negative":            false,
				}},
			})
		}
	}

	// Dayparting: [{day, start_hour, end_hour}].
	if entries, ok := t["dayparting"].([]any); ok {
		for _, raw := range entries {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			day := dayOfWeek(stringValue(m["day"]))
			start, sok := intValue(m["start_hour"])
			end, eok := intValue(m["end_hour"])
			if day == "" || !sok || !eok || start < 0 || end > 24 || start >= end {
				continue
			}
			out = append(out, targetingSpec{
				targetingType: targetingTime,
				key:           fmt.Sprintf("day:%s:%d:%d", day, start, end),
				details: map[string]any{"dayAndTimeDetails": map[string]any{
					"dayOfWeek":          day,
					"startHour":          start,
					"endHour":            end,
					"timeZoneResolution": "TIME_ZONE_RESOLUTION_END_USER",
				}},
			})
		}
	}

	return out
}

func (c *Client) geoSpecs(ctx context.Context, accessToken, advertiserID string, places []string) []targetingSpec {
	var out []targetingSpec
	cache := make(map[string]string)
	for _, place := range places {
		place = strings.TrimSpace(place)
		if place == "" {
			continue
		}
		id, ok := cache[place]
		if !ok {
			var err error
			id, err = c.searchGeoRegion(ctx, accessToken, advertiserID, place)
			if err != nil {
				out = append(out, targetingSpec{err: err})
				continue
			}
			cache[place] = id
		}
		if id == "" {
			continue
		}
		out = append(out, targetingSpec{
			targetingType: targetingGeo,
			key:           "geo:" + id,
			details:       map[string]any{"geoRegionDetails": map[string]any{"targetingOptionId": id, "negative": false}},
		})
	}
	return out
}

func (c *Client) searchGeoRegion(ctx context.Context, accessToken, advertiserID, query string) (string, error) {
	body := map[string]any{
		"advertiserId": advertiserID,
		"pageSize":     10,
		"geoRegionSearchTerms": map[string]any{
			"geoRegionQuery": query,
		},
	}
	var out struct {
		TargetingOptions []targetingOption `json:"targetingOptions"`
	}
	searchURL := c.apiURL("/targetingTypes/" + targetingGeo + "/targetingOptions:search")
	if err := c.postJSON(ctx, accessToken, searchURL, body, &out); err != nil {
		return "", fmt.Errorf("search dv360 geo region %q: %w", query, err)
	}
	if len(out.TargetingOptions) == 0 {
		return "", nil
	}
	for _, opt := range out.TargetingOptions {
		if opt.GeoRegionDetails != nil && opt.GeoRegionDetails.GeoRegionType == "GEO_REGION_TYPE_COUNTRY" {
			return opt.TargetingOptionID, nil
		}
	}
	return out.TargetingOptions[0].TargetingOptionID, nil
}

func (c *Client) listAssignedTargeting(ctx context.Context, accessToken, advertiserID, lineItemID string) (map[string]bool, error) {
	out := make(map[string]bool)
	for _, targetingType := range []string{
		targetingGeo, targetingAge, targetingGender, targetingDevice, targetingTime,
		targetingFirstParty, targetingGoogleAudience, targetingCombined, targetingCustomList, targetingLookalike,
	} {
		path := fmt.Sprintf("/advertisers/%s/lineItems/%s/targetingTypes/%s/assignedTargetingOptions",
			url.PathEscape(advertiserID), url.PathEscape(lineItemID), targetingType)
		items, err := c.listAll(ctx, accessToken, c.apiURL(path), "assignedTargetingOptions")
		if err != nil {
			return nil, fmt.Errorf("list dv360 %s targeting: %w", targetingType, err)
		}
		rows, err := decodeAll[assignedTargetingOption](items)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			switch {
			case r.GeoRegionDetails != nil:
				out["geo:"+r.GeoRegionDetails.TargetingOptionID] = true
			case r.AgeRangeDetails != nil:
				out["age:"+r.AgeRangeDetails.AgeRange] = true
			case r.GenderDetails != nil:
				out["gender:"+r.GenderDetails.Gender] = true
			case r.DeviceTypeDetails != nil:
				out["device:"+r.DeviceTypeDetails.DeviceType] = true
			case r.DayAndTimeDetails != nil:
				d := r.DayAndTimeDetails
				out[fmt.Sprintf("day:%s:%d:%d", d.DayOfWeek, d.StartHour, d.EndHour)] = true
			case r.FirstPartyDetails != nil:
				out["audience:fp:"+r.FirstPartyDetails.AudienceID] = true
			case r.GoogleAudienceDetails != nil:
				out["audience:google:"+r.GoogleAudienceDetails.GoogleAudienceType+":"+r.GoogleAudienceDetails.GoogleAudienceID] = true
			case r.CombinedAudienceDetails != nil:
				out["audience:combined:"+r.CombinedAudienceDetails.CombinedAudienceID] = true
			case r.CustomListDetails != nil:
				out["audience:customlist:"+r.CustomListDetails.CustomListType+":"+r.CustomListDetails.CustomListID] = true
			case r.LookalikeAudienceDetails != nil:
				out["audience:lookalike:"+r.LookalikeAudienceDetails.LookalikeAudienceID] = true
			}
		}
	}
	return out, nil
}

func (c *Client) assignTargeting(ctx context.Context, accessToken, advertiserID, lineItemID, targetingType string, details map[string]any) error {
	path := fmt.Sprintf("/advertisers/%s/lineItems/%s/targetingTypes/%s/assignedTargetingOptions",
		url.PathEscape(advertiserID), url.PathEscape(lineItemID), targetingType)
	if err := c.postJSON(ctx, accessToken, c.apiURL(path), details, nil); err != nil {
		return fmt.Errorf("assign dv360 %s targeting: %w", targetingType, err)
	}
	return nil
}

type ageBucket struct {
	min  int
	max  int
	enum string
}

var ageBuckets = []ageBucket{
	{18, 24, "AGE_RANGE_18_24"},
	{25, 34, "AGE_RANGE_25_34"},
	{35, 44, "AGE_RANGE_35_44"},
	{45, 54, "AGE_RANGE_45_54"},
	{55, 64, "AGE_RANGE_55_64"},
	{65, 200, "AGE_RANGE_65_PLUS"},
}

type audienceEntry struct {
	id           string
	audienceType string
}

// audienceEntries accepts [{id, type}] or ["id"] and normalizes both.
func audienceEntries(v any) ([]audienceEntry, bool) {
	var out []audienceEntry
	switch x := v.(type) {
	case []any:
		for _, raw := range x {
			switch e := raw.(type) {
			case string:
				if strings.TrimSpace(e) != "" {
					out = append(out, audienceEntry{id: e})
				}
			case map[string]any:
				id := stringValue(e["id"])
				if id == "" {
					id = stringValue(e["audience_id"])
				}
				if id != "" {
					out = append(out, audienceEntry{id: id, audienceType: stringValue(e["type"])})
				}
			}
		}
	case string:
		if strings.TrimSpace(x) != "" {
			out = append(out, audienceEntry{id: x})
		}
	}
	return out, len(out) > 0
}

func googleAudienceType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in_market", "in-market", "inmarket":
		return "GOOGLE_AUDIENCE_TYPE_IN_MARKET"
	case "life_event", "life-event", "lifeevent":
		return "GOOGLE_AUDIENCE_TYPE_LIFE_EVENT"
	case "extended_demographic", "extended-demographic":
		return "GOOGLE_AUDIENCE_TYPE_EXTENDED_DEMOGRAPHIC"
	default:
		return "GOOGLE_AUDIENCE_TYPE_AFFINITY"
	}
}

func customListType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "custom_intent", "intent":
		return "CUSTOM_LIST_TYPE_CUSTOM_INTENT"
	default:
		return "CUSTOM_LIST_TYPE_CUSTOM_AFFINITY"
	}
}

func deviceType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "computer", "desktop", "pc":
		return "DEVICE_TYPE_COMPUTER"
	case "mobile", "phone", "smartphone", "smart_phone":
		return "DEVICE_TYPE_SMART_PHONE"
	case "tablet":
		return "DEVICE_TYPE_TABLET"
	case "tv", "ctv", "connected_tv", "connectedtv":
		return "DEVICE_TYPE_CONNECTED_TV"
	case "connected_device", "connecteddevice":
		return "DEVICE_TYPE_CONNECTED_DEVICE"
	default:
		return ""
	}
}

func dayOfWeek(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "monday", "mon", "1":
		return "MONDAY"
	case "tuesday", "tue", "2":
		return "TUESDAY"
	case "wednesday", "wed", "3":
		return "WEDNESDAY"
	case "thursday", "thu", "4":
		return "THURSDAY"
	case "friday", "fri", "5":
		return "FRIDAY"
	case "saturday", "sat", "6":
		return "SATURDAY"
	case "sunday", "sun", "0", "7":
		return "SUNDAY"
	default:
		return ""
	}
}

func stringSlice(v any) ([]string, bool) {
	switch x := v.(type) {
	case []string:
		if len(x) > 0 {
			return x, true
		}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out, true
		}
	case string:
		if strings.TrimSpace(x) != "" {
			return []string{x}, true
		}
	}
	return nil, false
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func intValue(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case json.Number:
		n, err := strconv.Atoi(x.String())
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
