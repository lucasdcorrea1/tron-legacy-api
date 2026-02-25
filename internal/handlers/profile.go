package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	_ "image/png" // Para decodificar PNG
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"github.com/rwcarlsen/goexif/exif"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/image/draw"
)

// GetProfile godoc
// @Summary Buscar perfil do usuário
// @Description Retorna o perfil do usuário autenticado
// @Tags profile
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.Profile
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Profile not found"
// @Router /profile [get]
func GetProfile(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var profile models.Profile
	err := database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
	if err != nil {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(profile)
}

// UpdateProfile godoc
// @Summary Atualizar perfil do usuário
// @Description Atualiza os dados do perfil do usuário autenticado
// @Tags profile
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.UpdateProfileRequest true "Dados para atualizar"
// @Success 200 {object} models.Profile
// @Failure 400 {string} string "Invalid request body"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Profile not found"
// @Router /profile [put]
func UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build update document
	update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	setFields := update["$set"].(bson.M)

	if req.Name != "" {
		setFields["name"] = req.Name
	}
	if req.Avatar != "" {
		setFields["avatar"] = req.Avatar
	}
	if req.Bio != "" {
		setFields["bio"] = req.Bio
	}
	if req.Settings.Currency != "" {
		setFields["settings.currency"] = req.Settings.Currency
	}
	if req.Settings.Language != "" {
		setFields["settings.language"] = req.Settings.Language
	}
	if req.Settings.Theme.Mode != "" {
		setFields["settings.theme.mode"] = req.Settings.Theme.Mode
	}
	if req.Settings.Theme.PrimaryColor != "" {
		setFields["settings.theme.primary_color"] = req.Settings.Theme.PrimaryColor
	}
	if req.Settings.Theme.AccentColor != "" {
		setFields["settings.theme.accent_color"] = req.Settings.Theme.AccentColor
	}
	if req.Settings.FirstDayOfWeek != 0 {
		setFields["settings.first_day_of_week"] = req.Settings.FirstDayOfWeek
	}
	if req.Settings.DateFormat != "" {
		setFields["settings.date_format"] = req.Settings.DateFormat
	}

	result, err := database.Profiles().UpdateOne(ctx, bson.M{"user_id": userID}, update)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	// Return updated profile
	var profile models.Profile
	database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)

	// Log event and increment metrics
	middleware.IncProfileUpdate()
	slog.Info("profile_updated",
		"user_id", userID.Hex(),
		"fields_updated", len(setFields)-1, // -1 for updated_at
	)

	json.NewEncoder(w).Encode(profile)
}

// UploadAvatar godoc
// @Summary Upload de foto de perfil
// @Description Faz upload de uma imagem para o avatar do usuário. A imagem é redimensionada para 256x256 e comprimida.
// @Tags profile
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param avatar formData file true "Imagem do avatar (PNG ou JPEG, max 5MB)"
// @Success 200 {object} models.Profile
// @Failure 400 {string} string "Invalid image"
// @Failure 401 {string} string "Unauthorized"
// @Failure 413 {string} string "Image too large"
// @Router /profile/avatar [post]
func UploadAvatar(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Limite de 5MB
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

	// Parse multipart form
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		http.Error(w, "Image too large (max 5MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("avatar")
	if err != nil {
		http.Error(w, "No image provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Verificar tipo MIME
	contentType := header.Header.Get("Content-Type")
	if contentType != "image/jpeg" && contentType != "image/png" {
		http.Error(w, "Only JPEG and PNG images are allowed", http.StatusBadRequest)
		return
	}

	// Ler imagem
	imgData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read image", http.StatusBadRequest)
		return
	}

	// Decodificar imagem
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		http.Error(w, "Invalid image format", http.StatusBadRequest)
		return
	}

	// Aplicar correção de orientação EXIF
	img = applyExifOrientation(bytes.NewReader(imgData), img)

	// Redimensionar para 256x256 (thumbnail quadrado)
	resized := resizeImage(img, 256, 256)

	// Comprimir como JPEG
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 80}); err != nil {
		http.Error(w, "Failed to process image", http.StatusInternalServerError)
		return
	}

	// Converter para base64 com data URI
	base64Img := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	// Salvar no banco
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	update := bson.M{
		"$set": bson.M{
			"avatar":     base64Img,
			"updated_at": time.Now(),
		},
	}

	result, err := database.Profiles().UpdateOne(ctx, bson.M{"user_id": userID}, update)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if result.MatchedCount == 0 {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	// Retornar perfil atualizado
	var profile models.Profile
	database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)

	// Log event and increment metrics
	middleware.IncAvatarUpload()
	slog.Info("avatar_uploaded",
		"user_id", userID.Hex(),
		"size_bytes", len(base64Img),
	)

	json.NewEncoder(w).Encode(profile)
}

// resizeImage redimensiona a imagem mantendo aspect ratio e cortando para quadrado
func resizeImage(img image.Image, width, height int) image.Image {
	bounds := img.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// Calcular área de corte para quadrado
	var cropRect image.Rectangle
	if srcWidth > srcHeight {
		// Imagem mais larga - cortar laterais
		offset := (srcWidth - srcHeight) / 2
		cropRect = image.Rect(offset, 0, offset+srcHeight, srcHeight)
	} else {
		// Imagem mais alta - cortar topo/base
		offset := (srcHeight - srcWidth) / 2
		cropRect = image.Rect(0, offset, srcWidth, offset+srcWidth)
	}

	// Criar imagem de destino
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	// Usar interpolação de alta qualidade
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, cropRect, draw.Over, nil)

	return dst
}

// applyExifOrientation corrige a orientação da imagem baseado nos metadados EXIF
func applyExifOrientation(r io.Reader, img image.Image) image.Image {
	x, err := exif.Decode(r)
	if err != nil {
		return img // Sem EXIF, retorna imagem original
	}

	orientTag, err := x.Get(exif.Orientation)
	if err != nil {
		return img // Sem tag de orientação
	}

	orient, err := orientTag.Int(0)
	if err != nil {
		return img
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	switch orient {
	case 1:
		// Normal - sem alteração
		return img
	case 2:
		// Flip horizontal
		return flipHorizontal(img)
	case 3:
		// Rotação 180°
		return rotate180(img)
	case 4:
		// Flip vertical
		return flipVertical(img)
	case 5:
		// Transpose (flip horizontal + rotação 90° anti-horário)
		flipped := flipHorizontal(img)
		return rotate90CCW(flipped, h, w)
	case 6:
		// Rotação 90° horário
		return rotate90CW(img, h, w)
	case 7:
		// Transverse (flip horizontal + rotação 90° horário)
		flipped := flipHorizontal(img)
		return rotate90CW(flipped, h, w)
	case 8:
		// Rotação 90° anti-horário
		return rotate90CCW(img, h, w)
	}

	return img
}

func flipHorizontal(img image.Image) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, y, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return dst
}

func flipVertical(img image.Image) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, h-1-y, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return dst
}

func rotate180(img image.Image) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return dst
}

func rotate90CW(img image.Image, newW, newH int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return dst
}

func rotate90CCW(img image.Image, newW, newH int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return dst
}
