package dv360

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/integrations"
)

const (
	maxImageBytes = 10 << 20  // 10 MB
	maxVideoBytes = 200 << 20 // 200 MB
)

var mediaHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// createCreative uploads the creative media as a DV360 asset and creates a
// paused hosted creative. The line item then references the creative ID.
func (c *Client) createCreative(ctx context.Context, accessToken, advertiserID string, spec integrations.CampaignSpec) (string, error) {
	cr := spec.FirstVariantCreative()
	if cr == nil {
		return "", fmt.Errorf("dv360 requires a creative")
	}
	if strings.TrimSpace(cr.LinkURL) == "" {
		return "", fmt.Errorf("dv360 requires a creative destination link")
	}

	kind := "image"
	mediaURL := strings.TrimSpace(cr.ImageURL)
	if strings.EqualFold(cr.Format, "video") || (mediaURL == "" && cr.VideoURL != "") {
		kind = "video"
		mediaURL = strings.TrimSpace(cr.VideoURL)
	}
	if mediaURL == "" {
		return "", fmt.Errorf("dv360 requires an image_url or video_url")
	}

	data, contentType, filename, err := c.fetchCreativeMedia(ctx, mediaURL, kind)
	if err != nil {
		return "", err
	}

	width, height := 0, 0
	if kind == "image" {
		cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(data))
		if decodeErr != nil {
			return "", fmt.Errorf("decode creative image: %w", decodeErr)
		}
		width, height = cfg.Width, cfg.Height
	}

	assetID, err := c.uploadAsset(ctx, accessToken, advertiserID, filename, contentType, data)
	if err != nil {
		return "", err
	}

	creativeType := "CREATIVE_TYPE_STANDARD"
	if kind == "video" {
		creativeType = "CREATIVE_TYPE_VIDEO"
	}
	body := map[string]any{
		"displayName":   spec.Name + " - creative",
		"entityStatus":  "ENTITY_STATUS_PAUSED",
		"creativeType":  creativeType,
		"hostingSource": "HOSTING_SOURCE_HOSTED",
		"dimensions": map[string]any{
			"widthPixels":  width,
			"heightPixels": height,
		},
		"assets": []any{map[string]any{
			"asset": map[string]any{"mediaId": assetID},
			"role":  "ASSET_ROLE_MAIN",
		}},
		"exitEvents": []any{map[string]any{
			"type": "EXIT_EVENT_TYPE_DEFAULT",
			"url":  cr.LinkURL,
		}},
	}

	var out struct {
		CreativeID string `json:"creativeId"`
	}
	path := fmt.Sprintf("/advertisers/%s/creatives", url.PathEscape(advertiserID))
	if err := c.postJSON(ctx, accessToken, c.apiURL(path), body, &out); err != nil {
		return "", fmt.Errorf("create dv360 creative: %w", err)
	}
	if out.CreativeID == "" {
		return "", fmt.Errorf("dv360 creative create returned no creativeId")
	}
	return out.CreativeID, nil
}

// attachCreative associates an existing creative with a line item (used when a
// previous launch created the line item before the creative).
func (c *Client) attachCreative(ctx context.Context, accessToken, advertiserID, lineItemID, creativeID string) error {
	body := map[string]any{"creativeIds": []string{creativeID}}
	path := fmt.Sprintf("/advertisers/%s/lineItems/%s?updateMask=creativeIds",
		url.PathEscape(advertiserID), url.PathEscape(lineItemID))
	if err := c.patchJSON(ctx, accessToken, c.apiURL(path), body, nil); err != nil {
		return fmt.Errorf("attach dv360 creative to line item: %w", err)
	}
	return nil
}

func (c *Client) fetchCreativeMedia(ctx context.Context, rawURL, kind string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("build media request: %w", err)
	}
	resp, err := mediaHTTPClient.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("fetch creative media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("fetch creative media: status %d", resp.StatusCode)
	}

	limit := int64(maxImageBytes)
	if kind == "video" {
		limit = maxVideoBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read creative media: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, "", "", fmt.Errorf("creative media exceeds %d bytes", limit)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = http.DetectContentType(data)
	}
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	if contentType == "" {
		return nil, "", "", fmt.Errorf("could not determine creative media type")
	}

	filename := path.Base(rawURL)
	if i := strings.IndexByte(filename, '?'); i >= 0 {
		filename = filename[:i]
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = "creative"
	}
	if path.Ext(filename) == "" {
		exts, _ := mime.ExtensionsByType(contentType)
		if len(exts) > 0 {
			filename += exts[0]
		}
	}
	return data, contentType, filename, nil
}

// uploadAsset performs a Google multipart/related media upload and returns the
// DV360 media ID.
func (c *Client) uploadAsset(ctx context.Context, accessToken, advertiserID, filename, contentType string, data []byte) (string, error) {
	metadata, err := json.Marshal(map[string]string{"filename": filename})
	if err != nil {
		return "", fmt.Errorf("marshal asset metadata: %w", err)
	}

	body, boundary := multipartRelated(metadata, contentType, data)
	uploadURL := c.apiBaseURL + "/upload/" + apiVersion + "/advertisers/" +
		url.PathEscape(advertiserID) + "/assets?uploadType=multipart"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build asset upload: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)
	req.ContentLength = int64(len(body))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload dv360 asset: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read asset upload response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload dv360 asset: status %d: %s", resp.StatusCode, string(raw))
	}

	var out struct {
		Asset struct {
			MediaID string `json:"mediaId"`
		} `json:"asset"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode asset upload: %w", err)
	}
	if out.Asset.MediaID == "" {
		return "", fmt.Errorf("dv360 asset upload returned no mediaId")
	}
	return out.Asset.MediaID, nil
}

// multipartRelated builds a Google API multipart/related body:
//
//	--boundary
//	Content-Type: application/json; charset=UTF-8
//
//	{metadata}
//	--boundary
//	Content-Type: <media type>
//
//	<bytes>
//	--boundary--
func multipartRelated(metadata []byte, mediaType string, media []byte) ([]byte, string) {
	boundary := "pill4rs-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var buf bytes.Buffer
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: application/json; charset=UTF-8\r\n\r\n")
	buf.Write(metadata)
	buf.WriteString("\r\n--" + boundary + "\r\n")
	buf.WriteString("Content-Type: " + mediaType + "\r\n\r\n")
	buf.Write(media)
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	return buf.Bytes(), boundary
}
