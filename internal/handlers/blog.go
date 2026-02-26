package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/image/draw"
)

// ListPosts godoc
// @Summary Listar posts publicados
// @Description Retorna lista paginada de posts publicados com filtros opcionais
// @Tags blog
// @Produce json
// @Param page query int false "Página" default(1)
// @Param limit query int false "Itens por página" default(10)
// @Param category query string false "Filtrar por categoria"
// @Param tag query string false "Filtrar por tag"
// @Success 200 {object} models.PostListResponse
// @Router /blog/posts [get]
func ListPosts(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 10
	}

	filter := bson.M{"status": "published"}

	if category := r.URL.Query().Get("category"); category != "" {
		filter["category"] = category
	}
	if tag := r.URL.Query().Get("tag"); tag != "" {
		filter["tags"] = tag
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Count total
	total, err := database.Posts().CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, "Error counting posts", http.StatusInternalServerError)
		return
	}

	// Find posts with pagination
	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "published_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.Posts().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching posts", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var posts []models.BlogPost
	if err := cursor.All(ctx, &posts); err != nil {
		http.Error(w, "Error decoding posts", http.StatusInternalServerError)
		return
	}

	// Enrich with author info
	postResponses := enrichPostsWithAuthor(ctx, posts)

	response := models.PostListResponse{
		Posts: postResponses,
		Total: total,
		Page:  page,
		Limit: limit,
	}

	json.NewEncoder(w).Encode(response)
}

// GetPostBySlug godoc
// @Summary Buscar post por slug
// @Description Retorna um post publicado pelo slug. Autores autenticados podem ver seus rascunhos.
// @Tags blog
// @Produce json
// @Param slug path string true "Slug do post"
// @Success 200 {object} models.PostResponse
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{slug} [get]
func GetPostBySlug(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		http.Error(w, "Slug is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var post models.BlogPost
	err := database.Posts().FindOne(ctx, bson.M{"slug": slug}).Decode(&post)
	if err != nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	// If draft, only the author can see it
	if post.Status != "published" {
		userID := middleware.GetUserID(r)
		if userID == primitive.NilObjectID || userID != post.AuthorID {
			http.Error(w, "Post not found", http.StatusNotFound)
			return
		}
	}

	// Enrich with author info
	responses := enrichPostsWithAuthor(ctx, []models.BlogPost{post})
	if len(responses) == 0 {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(responses[0])
}

// CreatePost godoc
// @Summary Criar novo post
// @Description Cria um novo post no blog. Requer role admin ou author.
// @Tags blog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.CreatePostRequest true "Dados do post"
// @Success 201 {object} models.PostResponse
// @Failure 400 {string} string "Invalid request body"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Router /blog/posts [post]
func CreatePost(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req models.CreatePostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Title == "" || req.Content == "" {
		http.Error(w, "Title and content are required", http.StatusBadRequest)
		return
	}

	if req.Status == "" {
		req.Status = "draft"
	}
	if req.Status != "draft" && req.Status != "published" {
		http.Error(w, "Status must be 'draft' or 'published'", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	slug := generateSlug(req.Title)

	// Ensure slug is unique
	slug, err := ensureUniqueSlug(ctx, slug, primitive.NilObjectID)
	if err != nil {
		http.Error(w, "Error generating slug", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	post := models.BlogPost{
		ID:              primitive.NewObjectID(),
		AuthorID:        userID,
		Title:           req.Title,
		Slug:            slug,
		Content:         req.Content,
		Excerpt:         req.Excerpt,
		CoverImage:      req.CoverImage,
		Category:        req.Category,
		Tags:            req.Tags,
		Status:          req.Status,
		MetaTitle:       req.MetaTitle,
		MetaDescription: req.MetaDescription,
		ReadingTime:     estimateReadingTime(req.Content),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if post.Tags == nil {
		post.Tags = []string{}
	}

	if req.Status == "published" {
		post.PublishedAt = &now
	}

	_, err = database.Posts().InsertOne(ctx, post)
	if err != nil {
		http.Error(w, "Error creating post", http.StatusInternalServerError)
		return
	}

	middleware.IncPostCreated()
	slog.Info("post_created",
		"post_id", post.ID.Hex(),
		"author_id", userID.Hex(),
		"status", post.Status,
	)

	responses := enrichPostsWithAuthor(ctx, []models.BlogPost{post})
	w.WriteHeader(http.StatusCreated)
	if len(responses) > 0 {
		json.NewEncoder(w).Encode(responses[0])
	} else {
		json.NewEncoder(w).Encode(post)
	}
}

// UpdatePost godoc
// @Summary Atualizar post
// @Description Atualiza um post existente. Autores só editam seus posts, admins editam qualquer um.
// @Tags blog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID do post"
// @Param request body models.UpdatePostRequest true "Dados para atualizar"
// @Success 200 {object} models.PostResponse
// @Failure 400 {string} string "Invalid request body"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{id} [put]
func UpdatePost(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	postIDStr := r.PathValue("id")

	var req models.UpdatePostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Fetch existing post by ObjectID or slug
	var post models.BlogPost
	var filter bson.M
	postID, err := primitive.ObjectIDFromHex(postIDStr)
	if err == nil {
		filter = bson.M{"_id": postID}
	} else {
		filter = bson.M{"slug": postIDStr}
	}
	err = database.Posts().FindOne(ctx, filter).Decode(&post)
	if err != nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	// Check ownership: author can only edit own posts, admin can edit any
	if post.AuthorID != userID {
		var profile models.Profile
		err = database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
		if err != nil || profile.Role != "admin" {
			http.Error(w, "Forbidden: you can only edit your own posts", http.StatusForbidden)
			return
		}
	}

	// Build update
	update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	setFields := update["$set"].(bson.M)

	if req.Title != nil {
		setFields["title"] = *req.Title
		newSlug := generateSlug(*req.Title)
		newSlug, err = ensureUniqueSlug(ctx, newSlug, post.ID)
		if err == nil {
			setFields["slug"] = newSlug
		}
	}
	if req.Content != nil {
		setFields["content"] = *req.Content
		setFields["reading_time"] = estimateReadingTime(*req.Content)
	}
	if req.Excerpt != nil {
		setFields["excerpt"] = *req.Excerpt
	}
	if req.CoverImage != nil {
		setFields["cover_image"] = *req.CoverImage
	}
	if req.Category != nil {
		setFields["category"] = *req.Category
	}
	if req.Tags != nil {
		setFields["tags"] = req.Tags
	}
	if req.MetaTitle != nil {
		setFields["meta_title"] = *req.MetaTitle
	}
	if req.MetaDescription != nil {
		setFields["meta_description"] = *req.MetaDescription
	}
	if req.Status != nil {
		if *req.Status != "draft" && *req.Status != "published" {
			http.Error(w, "Status must be 'draft' or 'published'", http.StatusBadRequest)
			return
		}
		setFields["status"] = *req.Status
		// Set published_at when transitioning to published
		if *req.Status == "published" && post.PublishedAt == nil {
			now := time.Now()
			setFields["published_at"] = now
		}
	}

	_, err = database.Posts().UpdateOne(ctx, bson.M{"_id": post.ID}, update)
	if err != nil {
		http.Error(w, "Error updating post", http.StatusInternalServerError)
		return
	}

	// Return updated post
	var updated models.BlogPost
	database.Posts().FindOne(ctx, bson.M{"_id": post.ID}).Decode(&updated)

	middleware.IncPostUpdated()
	slog.Info("post_updated",
		"post_id", post.ID.Hex(),
		"user_id", userID.Hex(),
	)

	responses := enrichPostsWithAuthor(ctx, []models.BlogPost{updated})
	if len(responses) > 0 {
		json.NewEncoder(w).Encode(responses[0])
	} else {
		json.NewEncoder(w).Encode(updated)
	}
}

// DeletePost godoc
// @Summary Deletar post
// @Description Deleta um post. Autores só deletam seus posts, admins deletam qualquer um.
// @Tags blog
// @Produce json
// @Security BearerAuth
// @Param id path string true "ID do post"
// @Success 200 {string} string "Post deleted"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{id} [delete]
func DeletePost(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	postIDStr := r.PathValue("id")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Fetch post by ObjectID or slug
	var post models.BlogPost
	var filter bson.M
	postID, err := primitive.ObjectIDFromHex(postIDStr)
	if err == nil {
		filter = bson.M{"_id": postID}
	} else {
		filter = bson.M{"slug": postIDStr}
	}
	err = database.Posts().FindOne(ctx, filter).Decode(&post)
	if err != nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	// Check ownership
	if post.AuthorID != userID {
		var profile models.Profile
		err = database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
		if err != nil || profile.Role != "admin" {
			http.Error(w, "Forbidden: you can only delete your own posts", http.StatusForbidden)
			return
		}
	}

	_, err = database.Posts().DeleteOne(ctx, bson.M{"_id": post.ID})
	if err != nil {
		http.Error(w, "Error deleting post", http.StatusInternalServerError)
		return
	}

	middleware.IncPostDeleted()
	slog.Info("post_deleted",
		"post_id", post.ID.Hex(),
		"user_id", userID.Hex(),
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Post deleted"})
}

// MyPosts godoc
// @Summary Listar meus posts
// @Description Retorna todos os posts do autor autenticado (rascunhos + publicados)
// @Tags blog
// @Produce json
// @Security BearerAuth
// @Param page query int false "Página" default(1)
// @Param limit query int false "Itens por página" default(10)
// @Success 200 {object} models.PostListResponse
// @Failure 401 {string} string "Unauthorized"
// @Router /blog/posts/me [get]
func MyPosts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 10
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"author_id": userID}

	total, err := database.Posts().CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, "Error counting posts", http.StatusInternalServerError)
		return
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "updated_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.Posts().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching posts", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var posts []models.BlogPost
	if err := cursor.All(ctx, &posts); err != nil {
		http.Error(w, "Error decoding posts", http.StatusInternalServerError)
		return
	}

	postResponses := enrichPostsWithAuthor(ctx, posts)

	response := models.PostListResponse{
		Posts: postResponses,
		Total: total,
		Page:  page,
		Limit: limit,
	}

	json.NewEncoder(w).Encode(response)
}

// UploadPostImage godoc
// @Summary Upload de imagem para post
// @Description Faz upload de uma imagem, redimensiona para 800px de largura e comprime. Salva na collection images e retorna URL de servir.
// @Tags blog
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param image formData file true "Imagem (PNG ou JPEG, max 5MB)"
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Invalid image"
// @Failure 401 {string} string "Unauthorized"
// @Failure 413 {string} string "Image too large"
// @Router /blog/upload [post]
func UploadPostImage(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Limite de 5MB
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		http.Error(w, "Image too large (max 5MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "No image provided. Use field name 'image'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	imgData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read image", http.StatusBadRequest)
		return
	}

	// Detect real content type from file bytes (first 512 bytes)
	detectedType := http.DetectContentType(imgData)
	if detectedType != "image/jpeg" && detectedType != "image/png" && detectedType != "image/webp" {
		http.Error(w, "Only JPEG, PNG and WebP images are allowed", http.StatusBadRequest)
		return
	}

	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		http.Error(w, "Invalid image format", http.StatusBadRequest)
		return
	}

	// Redimensionar para max 800px de largura mantendo proporção
	resized := resizeCover(img, 800)

	// Comprimir como JPEG quality 65 (~30-50KB por imagem)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 65}); err != nil {
		http.Error(w, "Failed to process image", http.StatusInternalServerError)
		return
	}

	base64Img := base64.StdEncoding.EncodeToString(buf.Bytes())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Salvar na collection images
	imgDoc := models.BlogImage{
		ID:         primitive.NewObjectID(),
		UploaderID: userID,
		Data:       base64Img,
		Size:       buf.Len(),
		CreatedAt:  time.Now(),
	}

	_, err = database.Images().InsertOne(ctx, imgDoc)
	if err != nil {
		http.Error(w, "Error saving image", http.StatusInternalServerError)
		return
	}

	// Retornar URL de servir a imagem
	imageURL := "/api/v1/blog/images/" + imgDoc.ID.Hex()

	slog.Info("blog_image_uploaded",
		"image_id", imgDoc.ID.Hex(),
		"user_id", userID.Hex(),
		"original_size", len(imgData),
		"compressed_size", buf.Len(),
	)

	json.NewEncoder(w).Encode(map[string]string{
		"url": imageURL,
	})
}

// ServeImage godoc
// @Summary Servir imagem do blog
// @Description Retorna a imagem em bytes (JPEG). Público, com cache de 7 dias.
// @Tags blog
// @Produce jpeg
// @Param id path string true "ID da imagem"
// @Success 200 {file} binary
// @Failure 404 {string} string "Image not found"
// @Router /blog/images/{id} [get]
func ServeImage(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	imgID, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid image ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var imgDoc models.BlogImage
	err = database.Images().FindOne(ctx, bson.M{"_id": imgID}).Decode(&imgDoc)
	if err != nil {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}

	// Decodificar base64 para bytes
	imgBytes, err := base64.StdEncoding.DecodeString(imgDoc.Data)
	if err != nil {
		http.Error(w, "Error decoding image", http.StatusInternalServerError)
		return
	}

	// Headers de cache (7 dias) e content type
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(imgBytes)))
	w.Write(imgBytes)
}

// resizeCover redimensiona imagem mantendo proporção com largura máxima
func resizeCover(img image.Image, maxWidth int) image.Image {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	if srcW <= maxWidth {
		return img
	}

	newW := maxWidth
	newH := int(float64(srcH) * float64(maxWidth) / float64(srcW))

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	return dst
}

// enrichPostsWithAuthor adds author name and avatar to post responses
func enrichPostsWithAuthor(ctx context.Context, posts []models.BlogPost) []models.PostResponse {
	if len(posts) == 0 {
		return []models.PostResponse{}
	}

	// Collect unique author IDs
	authorIDs := make(map[primitive.ObjectID]bool)
	for _, p := range posts {
		authorIDs[p.AuthorID] = true
	}

	ids := make([]primitive.ObjectID, 0, len(authorIDs))
	for id := range authorIDs {
		ids = append(ids, id)
	}

	// Fetch profiles
	cursor, err := database.Profiles().Find(ctx, bson.M{"user_id": bson.M{"$in": ids}})
	profileMap := make(map[primitive.ObjectID]models.Profile)
	if err == nil {
		defer cursor.Close(ctx)
		var profiles []models.Profile
		if cursor.All(ctx, &profiles) == nil {
			for _, p := range profiles {
				profileMap[p.UserID] = p
			}
		}
	}

	responses := make([]models.PostResponse, len(posts))
	for i, post := range posts {
		resp := models.PostResponse{BlogPost: post}
		if profile, ok := profileMap[post.AuthorID]; ok {
			resp.AuthorName = profile.Name
			resp.AuthorAvatar = profile.Avatar
		}
		responses[i] = resp
	}

	return responses
}

// generateSlug creates a URL-friendly slug from a title
func generateSlug(title string) string {
	slug := strings.ToLower(title)

	// Replace accented characters
	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a", "ä", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"ó", "o", "ò", "o", "õ", "o", "ô", "o", "ö", "o",
		"ú", "u", "ù", "u", "û", "u", "ü", "u",
		"ç", "c", "ñ", "n",
	)
	slug = replacer.Replace(slug)

	// Replace non-alphanumeric with hyphens
	var b strings.Builder
	for _, r := range slug {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	slug = b.String()

	// Collapse multiple hyphens
	re := regexp.MustCompile(`-+`)
	slug = re.ReplaceAllString(slug, "-")

	// Trim hyphens from start and end
	slug = strings.Trim(slug, "-")

	return slug
}

// ensureUniqueSlug appends a number if the slug already exists
func ensureUniqueSlug(ctx context.Context, slug string, excludeID primitive.ObjectID) (string, error) {
	candidate := slug
	counter := 1

	for {
		filter := bson.M{"slug": candidate}
		if excludeID != primitive.NilObjectID {
			filter["_id"] = bson.M{"$ne": excludeID}
		}

		count, err := database.Posts().CountDocuments(ctx, filter)
		if err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
		counter++
		candidate = slug + "-" + strconv.Itoa(counter)
	}
}

// estimateReadingTime calculates reading time based on ~200 words per minute
func estimateReadingTime(content string) int {
	words := len(strings.Fields(content))
	minutes := words / 200
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}
