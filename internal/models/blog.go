package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// BlogPost represents a blog article
type BlogPost struct {
	ID              primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	AuthorID        primitive.ObjectID `json:"author_id" bson:"author_id"`
	Title           string             `json:"title" bson:"title"`
	Slug            string             `json:"slug" bson:"slug"`
	Content         string             `json:"content" bson:"content"`
	Excerpt         string             `json:"excerpt" bson:"excerpt"`
	CoverImage      string             `json:"cover_image,omitempty" bson:"cover_image,omitempty"`
	CoverImages     []string           `json:"cover_images,omitempty" bson:"cover_images,omitempty"` // array of group_ids for multi-image carousel
	Category        string             `json:"category" bson:"category"`
	Tags            []string           `json:"tags" bson:"tags"`
	Status          string             `json:"status" bson:"status"` // "draft" or "published"
	MetaTitle       string             `json:"meta_title,omitempty" bson:"meta_title,omitempty"`
	MetaDescription string             `json:"meta_description,omitempty" bson:"meta_description,omitempty"`
	ReadingTime     int                `json:"reading_time" bson:"reading_time"` // estimated minutes
	ViewCount       int64              `json:"view_count" bson:"view_count"`
	UniqueViewCount int64              `json:"unique_view_count" bson:"unique_view_count"`
	LikeCount       int64              `json:"like_count" bson:"like_count"`
	CommentCount    int64              `json:"comment_count" bson:"comment_count"`
	PublishedAt     *time.Time         `json:"published_at,omitempty" bson:"published_at,omitempty"`
	CreatedAt       time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at" bson:"updated_at"`
}

// CreatePostRequest is the request body for creating a blog post
type CreatePostRequest struct {
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Excerpt         string   `json:"excerpt"`
	CoverImage      string   `json:"cover_image,omitempty"`
	CoverImages     []string `json:"cover_images,omitempty"` // array of group_ids
	Category        string   `json:"category"`
	Tags            []string `json:"tags,omitempty"`
	Status          string   `json:"status"` // "draft" or "published"
	MetaTitle       string   `json:"meta_title,omitempty"`
	MetaDescription string   `json:"meta_description,omitempty"`
}

// UpdatePostRequest is the request body for updating a blog post
type UpdatePostRequest struct {
	Title           *string  `json:"title,omitempty"`
	Content         *string  `json:"content,omitempty"`
	Excerpt         *string  `json:"excerpt,omitempty"`
	CoverImage      *string  `json:"cover_image,omitempty"`
	CoverImages     []string `json:"cover_images,omitempty"` // array of group_ids
	Category        *string  `json:"category,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Status          *string  `json:"status,omitempty"`
	MetaTitle       *string  `json:"meta_title,omitempty"`
	MetaDescription *string  `json:"meta_description,omitempty"`
}

// PostResponse is the response for a single blog post with author info
type PostResponse struct {
	BlogPost    `json:",inline"`
	AuthorName  string `json:"author_name"`
	AuthorAvatar string `json:"author_avatar,omitempty"`
}

// PostListResponse is the paginated response for listing blog posts
type PostListResponse struct {
	Posts []PostResponse `json:"posts"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Limit int            `json:"limit"`
}

// BlogImage represents an uploaded image stored in the images collection
type BlogImage struct {
	ID         primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	UploaderID primitive.ObjectID `json:"uploader_id" bson:"uploader_id"`
	GroupID    string             `json:"group_id,omitempty" bson:"group_id,omitempty"`       // shared across size variants
	SizeLabel  string             `json:"size_label,omitempty" bson:"size_label,omitempty"`   // "thumb", "card", or "banner"
	Width      int                `json:"width,omitempty" bson:"width,omitempty"`             // image width in pixels
	Data       string             `json:"-" bson:"data"`                                      // base64 data, never in JSON list responses
	Size       int                `json:"size" bson:"size"`                                   // compressed size in bytes
	CreatedAt  time.Time          `json:"created_at" bson:"created_at"`
}
