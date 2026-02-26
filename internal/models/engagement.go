package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PostView tracks a unique user view on a post
type PostView struct {
	ID       primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	PostID   primitive.ObjectID `json:"post_id" bson:"post_id"`
	UserID   primitive.ObjectID `json:"user_id" bson:"user_id"`
	ViewedAt time.Time          `json:"viewed_at" bson:"viewed_at"`
}

// PostLike represents a user liking a post
type PostLike struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	PostID    primitive.ObjectID `json:"post_id" bson:"post_id"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
}

// PostComment represents a user comment on a post
type PostComment struct {
	ID        primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	PostID    primitive.ObjectID `json:"post_id" bson:"post_id"`
	UserID    primitive.ObjectID `json:"user_id" bson:"user_id"`
	Content   string             `json:"content" bson:"content"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time          `json:"updated_at" bson:"updated_at"`
}

// CreateCommentRequest is the request body for creating a comment
type CreateCommentRequest struct {
	Content string `json:"content"`
}

// CommentResponse is a comment with author info
type CommentResponse struct {
	PostComment  `json:",inline"`
	AuthorName   string `json:"author_name"`
	AuthorAvatar string `json:"author_avatar,omitempty"`
}

// CommentListResponse is a paginated list of comments
type CommentListResponse struct {
	Comments []CommentResponse `json:"comments"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	Limit    int               `json:"limit"`
}

// LikeResponse is the response for like/unlike toggle
type LikeResponse struct {
	Liked     bool  `json:"liked"`
	LikeCount int64 `json:"like_count"`
}

// PostStatsResponse contains engagement stats for a post
type PostStatsResponse struct {
	ViewCount       int64 `json:"view_count"`
	UniqueViewCount int64 `json:"unique_view_count"`
	LikeCount       int64 `json:"like_count"`
	CommentCount    int64 `json:"comment_count"`
	Liked           bool  `json:"liked"`
}
