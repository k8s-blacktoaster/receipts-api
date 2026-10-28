package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg" // register decoders for validateImage
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
	"receipts-db/internal/storage"
)

// DefaultMaxUploadBytes matches xAI's 20 MiB image limit.
const DefaultMaxUploadBytes = 20 << 20

// allowedImageTypes: formats the vision provider accepts (xAI: jpeg, png).
var allowedImageTypes = map[string]string{"image/jpeg": ".jpg", "image/png": ".png"}

// maxImagePixels bounds decoded image size (decompression-bomb guard); ~50 MP.
const maxImagePixels = 50_000_000

// validateImage fully decodes the image so truncated or corrupt JPEG/PNG files are rejected
// before anything is written or sent to a provider.
func validateImage(data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errors.New("Corrupt or unreadable image")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return errors.New("Image dimensions out of range")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return errors.New("Corrupt or truncated image")
	}
	return nil
}

// Enqueuer schedules extraction for a receipt.
type Enqueuer interface{ Enqueue(receiptID int) bool }

// UploadStore is the persistence the upload handler needs.
type UploadStore interface {
	FindReceiptBySHA(ctx context.Context, sha []byte) (receiptID int, docID int64, status string, found bool, err error)
	CreateUpload(ctx context.Context, doc *models.SourceDocument, receipt *models.Receipt) error
	MemberExists(ctx context.Context, id int64) (bool, error)
}

// ReceiptLookup is used by the re-extract endpoint.
type ReceiptLookup interface {
	GetByID(ctx context.Context, id int) (*models.Receipt, error)
	RequestReextract(ctx context.Context, id int) error
}

// ReextractError maps a RequestReextract error to an HTTP status and message (shared with the web UI).
func ReextractError(err error) (int, string) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return http.StatusNotFound, "Receipt not found"
	case errors.Is(err, repository.ErrNoPhoto):
		return http.StatusBadRequest, "Receipt has no photo"
	case errors.Is(err, repository.ErrReceiptBusy):
		return http.StatusConflict, "Extraction already in progress"
	case errors.Is(err, repository.ErrInvalidTransition):
		return http.StatusConflict, "Receipt is reviewed; re-extraction is not allowed (v1 has no force option)"
	default:
		return http.StatusInternalServerError, "Failed to queue extraction"
	}
}

type UploadHandler struct {
	Store    UploadStore
	Receipts ReceiptLookup
	Blobs    storage.BlobStore
	Queue    Enqueuer
	MaxBytes int64
	Logger   *slog.Logger
}

func (h *UploadHandler) respond(w http.ResponseWriter, code int, msg string, data any) {
	rh := &ReceiptHandler{logger: h.Logger}
	if code >= 400 {
		rh.respondWithError(w, code, msg)
		return
	}
	rh.respondWithJSON(w, code, models.NewSuccessResponse(code, msg, data))
}

func optionalID(r *http.Request, field string) (*int64, error) {
	v := r.FormValue(field)
	if v == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("invalid " + field)
	}
	return &id, nil
}

// UploadReceiptPhoto handles POST /api/v1/uploads
//
//	@Summary		Upload a receipt photo
//	@Description	Stores the image, creates a receipt with status "uploaded" and queues extraction. An identical image (same SHA-256) returns the existing receipt with 200.
//	@Tags			uploads
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			image			formData	file	true	"Receipt photo (image/jpeg or image/png, max 20 MiB)"
//	@Param			uploaded_by_id	formData	int		false	"Member who uploaded the photo"
//	@Param			member_id		formData	int		false	"Member whose purchase this is"
//	@Success		202				{object}	models.SuccessResponse{data=models.UploadResult}	"Accepted; extraction queued"
//	@Success		200				{object}	models.SuccessResponse{data=models.UploadResult}	"Duplicate image; existing receipt"
//	@Failure		400				{object}	models.ErrorResponse	"Bad request"
//	@Failure		413				{object}	models.ErrorResponse	"Image too large"
//	@Failure		415				{object}	models.ErrorResponse	"Unsupported image type"
//	@Failure		500				{object}	models.ErrorResponse	"Internal server error"
//	@Router			/uploads [post]
func (h *UploadHandler) UploadReceiptPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	max := h.MaxBytes
	if max <= 0 {
		max = DefaultMaxUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, max+1<<20) // allow multipart overhead
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			h.respond(w, http.StatusRequestEntityTooLarge, "Image too large", nil)
			return
		}
		h.respond(w, http.StatusBadRequest, "Expected multipart/form-data with an \"image\" file", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("image")
	if err != nil {
		h.respond(w, http.StatusBadRequest, "Missing \"image\" file", nil)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		h.respond(w, http.StatusBadRequest, "Could not read image", nil)
		return
	}
	if int64(len(data)) > max {
		h.respond(w, http.StatusRequestEntityTooLarge, "Image too large", nil)
		return
	}
	if len(data) == 0 {
		h.respond(w, http.StatusBadRequest, "Empty image", nil)
		return
	}
	mime := http.DetectContentType(data)
	ext, ok := allowedImageTypes[mime]
	if !ok {
		h.respond(w, http.StatusUnsupportedMediaType, "Unsupported image type (use JPEG or PNG)", nil)
		return
	}
	uploader, err := optionalID(r, "uploaded_by_id")
	if err != nil {
		h.respond(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	member, err := optionalID(r, "member_id")
	if err != nil {
		h.respond(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err := validateImage(data); err != nil {
		h.respond(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	// validate references before writing anything to disk
	for _, ref := range []struct {
		field string
		id    *int64
	}{{"uploaded_by_id", uploader}, {"member_id", member}} {
		if ref.id == nil {
			continue
		}
		ok, err := h.Store.MemberExists(ctx, *ref.id)
		if err != nil {
			h.Logger.Error("member lookup failed", "error", err)
			h.respond(w, http.StatusInternalServerError, "Failed to store upload", nil)
			return
		}
		if !ok {
			h.respond(w, http.StatusBadRequest, "Unknown "+ref.field, nil)
			return
		}
	}

	sum := sha256.Sum256(data)
	if rid, did, status, found, err := h.Store.FindReceiptBySHA(ctx, sum[:]); err != nil {
		h.Logger.Error("upload lookup failed", "error", err)
		h.respond(w, http.StatusInternalServerError, "Failed to store upload", nil)
		return
	} else if found {
		h.respond(w, http.StatusOK, "Duplicate image; existing receipt returned",
			models.UploadResult{ReceiptID: rid, SourceDocumentID: did, Status: status, Duplicate: true})
		return
	}

	hexSum := hex.EncodeToString(sum[:])
	key := "receipts/" + hexSum[:2] + "/" + hexSum + ext
	created, err := h.Blobs.Put(ctx, key, data)
	if err != nil {
		h.Logger.Error("blob put failed", "error", err)
		h.respond(w, http.StatusInternalServerError, "Failed to store upload", nil)
		return
	}
	doc := &models.SourceDocument{Kind: "photo", ObjectKey: key, SHA256: sum[:], MimeType: mime, UploadedByID: uploader}
	rc := &models.Receipt{Source: models.SourcePhoto, Status: models.StatusUploaded, UploadedByID: uploader, MemberID: member}
	if err := h.Store.CreateUpload(ctx, doc, rc); err != nil {
		// a concurrent identical upload may have won the unique sha256 race
		if rid, did, status, found, e := h.Store.FindReceiptBySHA(ctx, sum[:]); e == nil && found {
			h.respond(w, http.StatusOK, "Duplicate image; existing receipt returned",
				models.UploadResult{ReceiptID: rid, SourceDocumentID: did, Status: status, Duplicate: true})
			return
		}
		// Remove the file only if this request created it; an existing blob (same sha256)
		// may belong to another, successful upload.
		if created {
			if e := h.Blobs.Delete(ctx, key); e != nil {
				h.Logger.Warn("orphan blob cleanup failed", "key", key, "error", e)
			}
		}
		h.Logger.Error("create upload failed", "error", err)
		h.respond(w, http.StatusInternalServerError, "Failed to store upload", nil)
		return
	}
	msg := "Upload accepted; extraction disabled (stays uploaded until a provider is configured)"
	if h.Queue != nil {
		h.Queue.Enqueue(rc.ID)
		msg = "Upload accepted; extraction queued"
	}
	h.Logger.Info("receipt photo uploaded", "receipt_id", rc.ID, "source_document_id", doc.ID, "bytes", len(data))
	h.respond(w, http.StatusAccepted, msg,
		models.UploadResult{ReceiptID: rc.ID, SourceDocumentID: doc.ID, Status: rc.Status})
}

// ReextractReceipt handles POST /api/v1/receipts/{id}/extract
//
//	@Summary		Re-run extraction
//	@Description	Queues extraction again for a photo receipt in uploaded, parsed, needs_review or failed status. Overwrites parsed items.
//	@Description	Returns 409 while the receipt is being parsed, and always 409 for a "reviewed" receipt: v1 never
//	@Description	overwrites human-reviewed data and has no force option (reopen it to needs_review first).
//	@Tags			uploads
//	@Produce		json
//	@Param			id	path		int	true	"Receipt ID"
//	@Success		202	{object}	models.SuccessResponse{data=models.UploadResult}	"Queued"
//	@Failure		400	{object}	models.ErrorResponse	"Not a photo receipt"
//	@Failure		404	{object}	models.ErrorResponse	"Receipt not found"
//	@Failure		409	{object}	models.ErrorResponse	"Extraction in progress, or receipt already reviewed"
//	@Failure		503	{object}	models.ErrorResponse	"Extraction disabled"
//	@Router			/receipts/{id}/extract [post]
func (h *UploadHandler) ReextractReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		h.respond(w, http.StatusBadRequest, "Invalid receipt ID", nil)
		return
	}
	rc, err := h.Receipts.GetByID(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		h.respond(w, http.StatusNotFound, "Receipt not found", nil)
		return
	}
	if err != nil {
		h.respond(w, http.StatusInternalServerError, "Failed to load receipt", nil)
		return
	}
	if rc.SourceDocumentID == nil {
		h.respond(w, http.StatusBadRequest, "Receipt has no photo", nil)
		return
	}
	// Advisory fast-fail; the pipeline's atomic DB claim is what actually prevents double parsing.
	switch rc.Status {
	case models.StatusParsing:
		h.respond(w, http.StatusConflict, "Extraction already in progress", nil)
		return
	case models.StatusReviewed:
		h.respond(w, http.StatusConflict, "Receipt is reviewed; re-extraction is not allowed (v1 has no force option)", nil)
		return
	}
	if h.Queue == nil {
		h.respond(w, http.StatusServiceUnavailable, "Extraction disabled", nil)
		return
	}
	// Move to "uploaded" first: from here on edits are refused (409) until the new parse lands.
	if err := h.Receipts.RequestReextract(r.Context(), id); err != nil {
		code, msg := ReextractError(err)
		h.respond(w, code, msg, nil)
		return
	}
	h.Queue.Enqueue(id)
	h.respond(w, http.StatusAccepted, "Extraction queued",
		models.UploadResult{ReceiptID: id, SourceDocumentID: *rc.SourceDocumentID, Status: models.StatusUploaded})
}
