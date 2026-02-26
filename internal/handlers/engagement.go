package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// resolvePostBySlug finds a published post by slug, returns nil if not found
func resolvePostBySlug(ctx context.Context, slug string) *models.BlogPost {
	var post models.BlogPost
	err := database.Posts().FindOne(ctx, bson.M{"slug": slug, "status": "published"}).Decode(&post)
	if err != nil {
		return nil
	}
	return &post
}

// enrichCommentsWithAuthor adds author name and avatar to comment responses
func enrichCommentsWithAuthor(ctx context.Context, comments []models.PostComment) []models.CommentResponse {
	if len(comments) == 0 {
		return []models.CommentResponse{}
	}

	// Collect unique author IDs
	authorIDs := make(map[primitive.ObjectID]bool)
	for _, c := range comments {
		authorIDs[c.UserID] = true
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

	responses := make([]models.CommentResponse, len(comments))
	for i, comment := range comments {
		resp := models.CommentResponse{PostComment: comment}
		if profile, ok := profileMap[comment.UserID]; ok {
			resp.AuthorName = profile.Name
			resp.AuthorAvatar = profile.Avatar
		}
		responses[i] = resp
	}

	return responses
}

// RecordView godoc
// @Summary Registrar visualização de post
// @Description Incrementa view_count. Se autenticado, também registra view única.
// @Tags engagement
// @Produce json
// @Param slug path string true "Slug do post"
// @Success 200 {object} map[string]string
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{slug}/view [post]
func RecordView(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		http.Error(w, "Slug is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	post := resolvePostBySlug(ctx, slug)
	if post == nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	// Always increment view_count
	_, err := database.Posts().UpdateOne(ctx,
		bson.M{"_id": post.ID},
		bson.M{"$inc": bson.M{"view_count": 1}},
	)
	if err != nil {
		http.Error(w, "Error recording view", http.StatusInternalServerError)
		return
	}

	middleware.IncPostView()

	// If authenticated, track unique view
	userID := middleware.GetUserID(r)
	if userID != primitive.NilObjectID {
		result, err := database.PostViews().UpdateOne(ctx,
			bson.M{"post_id": post.ID, "user_id": userID},
			bson.M{"$setOnInsert": bson.M{
				"_id":       primitive.NewObjectID(),
				"post_id":   post.ID,
				"user_id":   userID,
				"viewed_at": time.Now(),
			}},
			options.Update().SetUpsert(true),
		)
		if err == nil && result.UpsertedCount > 0 {
			// New unique view — increment unique_view_count
			database.Posts().UpdateOne(ctx,
				bson.M{"_id": post.ID},
				bson.M{"$inc": bson.M{"unique_view_count": 1}},
			)
		}
	}

	slog.Info("post_view_recorded",
		"post_id", post.ID.Hex(),
		"slug", slug,
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "View recorded"})
}

// GetPostStats godoc
// @Summary Estatísticas de engajamento do post
// @Description Retorna view_count, unique_view_count, like_count, comment_count e se o usuário deu like
// @Tags engagement
// @Produce json
// @Param slug path string true "Slug do post"
// @Success 200 {object} models.PostStatsResponse
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{slug}/stats [get]
func GetPostStats(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		http.Error(w, "Slug is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	post := resolvePostBySlug(ctx, slug)
	if post == nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	stats := models.PostStatsResponse{
		ViewCount:       post.ViewCount,
		UniqueViewCount: post.UniqueViewCount,
		LikeCount:       post.LikeCount,
		CommentCount:    post.CommentCount,
	}

	// Check if current user liked this post
	userID := middleware.GetUserID(r)
	if userID != primitive.NilObjectID {
		count, err := database.PostLikes().CountDocuments(ctx, bson.M{
			"post_id": post.ID,
			"user_id": userID,
		})
		if err == nil && count > 0 {
			stats.Liked = true
		}
	}

	json.NewEncoder(w).Encode(stats)
}

// ToggleLike godoc
// @Summary Toggle like/unlike em post
// @Description Se já deu like, remove. Se não, adiciona. Requer autenticação.
// @Tags engagement
// @Produce json
// @Security BearerAuth
// @Param slug path string true "Slug do post"
// @Success 200 {object} models.LikeResponse
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{slug}/like [post]
func ToggleLike(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		http.Error(w, "Slug is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	post := resolvePostBySlug(ctx, slug)
	if post == nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	// Try to delete existing like
	result, err := database.PostLikes().DeleteOne(ctx, bson.M{
		"post_id": post.ID,
		"user_id": userID,
	})
	if err != nil {
		http.Error(w, "Error toggling like", http.StatusInternalServerError)
		return
	}

	var liked bool
	if result.DeletedCount > 0 {
		// Unlike: decrement like_count
		database.Posts().UpdateOne(ctx,
			bson.M{"_id": post.ID},
			bson.M{"$inc": bson.M{"like_count": -1}},
		)
		liked = false
		middleware.IncPostUnlike()
		slog.Info("post_unliked", "post_id", post.ID.Hex(), "user_id", userID.Hex())
	} else {
		// Like: insert and increment
		_, err := database.PostLikes().InsertOne(ctx, models.PostLike{
			ID:        primitive.NewObjectID(),
			PostID:    post.ID,
			UserID:    userID,
			CreatedAt: time.Now(),
		})
		if err != nil {
			// Could be duplicate key if race condition — treat as already liked
			http.Error(w, "Error toggling like", http.StatusInternalServerError)
			return
		}
		database.Posts().UpdateOne(ctx,
			bson.M{"_id": post.ID},
			bson.M{"$inc": bson.M{"like_count": 1}},
		)
		liked = true
		middleware.IncPostLike()
		slog.Info("post_liked", "post_id", post.ID.Hex(), "user_id", userID.Hex())
	}

	// Fetch updated like_count
	var updated models.BlogPost
	database.Posts().FindOne(ctx, bson.M{"_id": post.ID}).Decode(&updated)

	json.NewEncoder(w).Encode(models.LikeResponse{
		Liked:     liked,
		LikeCount: updated.LikeCount,
	})
}

// ListComments godoc
// @Summary Listar comentários de um post
// @Description Retorna lista paginada de comentários com info do autor
// @Tags engagement
// @Produce json
// @Param slug path string true "Slug do post"
// @Param page query int false "Página" default(1)
// @Param limit query int false "Itens por página" default(20)
// @Success 200 {object} models.CommentListResponse
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{slug}/comments [get]
func ListComments(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		http.Error(w, "Slug is required", http.StatusBadRequest)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	post := resolvePostBySlug(ctx, slug)
	if post == nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	filter := bson.M{"post_id": post.ID}

	total, err := database.PostComments().CountDocuments(ctx, filter)
	if err != nil {
		http.Error(w, "Error counting comments", http.StatusInternalServerError)
		return
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := database.PostComments().Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, "Error fetching comments", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var comments []models.PostComment
	if err := cursor.All(ctx, &comments); err != nil {
		http.Error(w, "Error decoding comments", http.StatusInternalServerError)
		return
	}

	commentResponses := enrichCommentsWithAuthor(ctx, comments)

	json.NewEncoder(w).Encode(models.CommentListResponse{
		Comments: commentResponses,
		Total:    total,
		Page:     page,
		Limit:    limit,
	})
}

// CreateComment godoc
// @Summary Criar comentário em post
// @Description Cria um novo comentário. Requer autenticação.
// @Tags engagement
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param slug path string true "Slug do post"
// @Param request body models.CreateCommentRequest true "Conteúdo do comentário"
// @Success 201 {object} models.CommentResponse
// @Failure 400 {string} string "Invalid request"
// @Failure 401 {string} string "Unauthorized"
// @Failure 404 {string} string "Post not found"
// @Router /blog/posts/{slug}/comments [post]
func CreateComment(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		http.Error(w, "Slug is required", http.StatusBadRequest)
		return
	}

	var req models.CreateCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Content) < 1 || len(req.Content) > 2000 {
		http.Error(w, "Content must be between 1 and 2000 characters", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	post := resolvePostBySlug(ctx, slug)
	if post == nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	now := time.Now()
	comment := models.PostComment{
		ID:        primitive.NewObjectID(),
		PostID:    post.ID,
		UserID:    userID,
		Content:   req.Content,
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := database.PostComments().InsertOne(ctx, comment)
	if err != nil {
		http.Error(w, "Error creating comment", http.StatusInternalServerError)
		return
	}

	// Increment comment_count
	database.Posts().UpdateOne(ctx,
		bson.M{"_id": post.ID},
		bson.M{"$inc": bson.M{"comment_count": 1}},
	)

	middleware.IncCommentCreated()
	slog.Info("comment_created",
		"comment_id", comment.ID.Hex(),
		"post_id", post.ID.Hex(),
		"user_id", userID.Hex(),
	)

	responses := enrichCommentsWithAuthor(ctx, []models.PostComment{comment})
	w.WriteHeader(http.StatusCreated)
	if len(responses) > 0 {
		json.NewEncoder(w).Encode(responses[0])
	} else {
		json.NewEncoder(w).Encode(comment)
	}
}

// DeleteComment godoc
// @Summary Deletar comentário
// @Description Autor do comentário, autor do post ou admin podem deletar.
// @Tags engagement
// @Produce json
// @Security BearerAuth
// @Param slug path string true "Slug do post"
// @Param id path string true "ID do comentário"
// @Success 200 {object} map[string]string
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Comment not found"
// @Router /blog/posts/{slug}/comments/{id} [delete]
func DeleteComment(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if userID == primitive.NilObjectID {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	slug := r.PathValue("slug")
	commentIDStr := r.PathValue("id")
	commentID, err := primitive.ObjectIDFromHex(commentIDStr)
	if err != nil {
		http.Error(w, "Invalid comment ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	post := resolvePostBySlug(ctx, slug)
	if post == nil {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	// Fetch the comment
	var comment models.PostComment
	err = database.PostComments().FindOne(ctx, bson.M{"_id": commentID, "post_id": post.ID}).Decode(&comment)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			http.Error(w, "Comment not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Error fetching comment", http.StatusInternalServerError)
		return
	}

	// Authorization: comment author, post author, or admin
	canDelete := false
	if comment.UserID == userID {
		canDelete = true
	} else if post.AuthorID == userID {
		canDelete = true
	} else {
		// Check if admin
		var profile models.Profile
		err = database.Profiles().FindOne(ctx, bson.M{"user_id": userID}).Decode(&profile)
		if err == nil && profile.Role == "admin" {
			canDelete = true
		}
	}

	if !canDelete {
		http.Error(w, "Forbidden: you cannot delete this comment", http.StatusForbidden)
		return
	}

	_, err = database.PostComments().DeleteOne(ctx, bson.M{"_id": commentID})
	if err != nil {
		http.Error(w, "Error deleting comment", http.StatusInternalServerError)
		return
	}

	// Decrement comment_count
	database.Posts().UpdateOne(ctx,
		bson.M{"_id": post.ID},
		bson.M{"$inc": bson.M{"comment_count": -1}},
	)

	middleware.IncCommentDeleted()
	slog.Info("comment_deleted",
		"comment_id", commentID.Hex(),
		"post_id", post.ID.Hex(),
		"user_id", userID.Hex(),
	)

	json.NewEncoder(w).Encode(map[string]string{"message": "Comment deleted"})
}
