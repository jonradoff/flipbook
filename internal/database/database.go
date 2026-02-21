package database

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// flipbookDoc is the MongoDB document shape for flipbooks.
type flipbookDoc struct {
	ID           string     `bson:"_id"`
	Title        string     `bson:"title"`
	Slug         string     `bson:"slug"`
	Description  string     `bson:"description"`
	Filename     string     `bson:"filename"`
	FileSize     int64      `bson:"file_size"`
	PageCount    int        `bson:"page_count"`
	Status       string     `bson:"status"`
	ErrorMessage string     `bson:"error_message"`
	PageWidth    int        `bson:"page_width"`
	PageHeight   int        `bson:"page_height"`
	IsPublic     bool       `bson:"is_public"`
	GridFSFileID string     `bson:"gridfs_file_id,omitempty"`
	CreatedAt    time.Time  `bson:"created_at"`
	UpdatedAt    time.Time  `bson:"updated_at"`
	ConvertedAt  *time.Time `bson:"converted_at,omitempty"`
	DeletedAt    *time.Time `bson:"deleted_at,omitempty"`
}

type viewDoc struct {
	FlipbookID string    `bson:"flipbook_id"`
	ViewedAt   time.Time `bson:"viewed_at"`
	Referrer   string    `bson:"referrer"`
	UserAgent  string    `bson:"user_agent"`
}

func docToModel(d *flipbookDoc) *models.Flipbook {
	return &models.Flipbook{
		ID:           d.ID,
		Title:        d.Title,
		Slug:         d.Slug,
		Description:  d.Description,
		Filename:     d.Filename,
		FileSize:     d.FileSize,
		PageCount:    d.PageCount,
		Status:       d.Status,
		ErrorMessage: d.ErrorMessage,
		PageWidth:    d.PageWidth,
		PageHeight:   d.PageHeight,
		IsPublic:     d.IsPublic,
		GridFSFileID: d.GridFSFileID,
		CreatedAt:    d.CreatedAt,
		UpdatedAt:    d.UpdatedAt,
		ConvertedAt:  d.ConvertedAt,
	}
}

type settingDoc struct {
	Key   string `bson:"_id"`
	Value string `bson:"value"`
}

type sessionDoc struct {
	Token     string    `bson:"_id"`
	CreatedAt time.Time `bson:"created_at"`
	ExpiresAt time.Time `bson:"expires_at"`
}

type DB struct {
	client    *mongo.Client
	flipbooks *mongo.Collection
	views     *mongo.Collection
	settings  *mongo.Collection
	sessions  *mongo.Collection
	gridfs    *mongo.GridFSBucket
}

func Open(ctx context.Context, uri, dbName string) (*DB, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	db := client.Database(dbName)
	d := &DB{
		client:    client,
		flipbooks: db.Collection("flipbooks"),
		views:     db.Collection("views"),
		settings:  db.Collection("settings"),
		sessions:  db.Collection("sessions"),
		gridfs:    db.GridFSBucket(),
	}

	d.ensureIndexes(ctx)
	return d, nil
}

func (d *DB) Close(ctx context.Context) error {
	return d.client.Disconnect(ctx)
}

func (d *DB) ensureIndexes(ctx context.Context) {
	d.flipbooks.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: -1}}},
	})
	d.views.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "flipbook_id", Value: 1}},
	})
	// TTL index to auto-expire sessions
	d.sessions.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
}

var notDeleted = bson.M{"deleted_at": nil}

func (d *DB) CreateFlipbook(fb *models.Flipbook) error {
	now := time.Now()
	doc := flipbookDoc{
		ID:          fb.ID,
		Title:       fb.Title,
		Slug:        fb.Slug,
		Description: fb.Description,
		Filename:    fb.Filename,
		FileSize:    fb.FileSize,
		Status:      fb.Status,
		IsPublic:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := d.flipbooks.InsertOne(context.Background(), doc)
	return err
}

func (d *DB) GetFlipbook(id string) (*models.Flipbook, error) {
	var doc flipbookDoc
	filter := bson.M{"_id": id, "deleted_at": nil}
	err := d.flipbooks.FindOne(context.Background(), filter).Decode(&doc)
	if err != nil {
		return nil, err
	}
	return docToModel(&doc), nil
}

func (d *DB) GetFlipbookBySlug(slug string) (*models.Flipbook, error) {
	var doc flipbookDoc
	filter := bson.M{"slug": slug, "deleted_at": nil}
	err := d.flipbooks.FindOne(context.Background(), filter).Decode(&doc)
	if err != nil {
		return nil, err
	}
	return docToModel(&doc), nil
}

func (d *DB) ListFlipbooks() ([]*models.Flipbook, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := d.flipbooks.Find(context.Background(), notDeleted, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	var flipbooks []*models.Flipbook
	for cursor.Next(context.Background()) {
		var doc flipbookDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		flipbooks = append(flipbooks, docToModel(&doc))
	}
	return flipbooks, nil
}

func (d *DB) UpdateStatus(id, status, errorMsg string) error {
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{
		"$set": bson.M{
			"status":        status,
			"error_message": errorMsg,
			"updated_at":    time.Now(),
		},
	})
	return err
}

func (d *DB) UpdateConversionResult(id string, pageCount, width, height int) error {
	now := time.Now()
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{
		"$set": bson.M{
			"page_count":    pageCount,
			"page_width":    width,
			"page_height":   height,
			"status":        models.StatusReady,
			"error_message": "",
			"converted_at":  now,
			"updated_at":    now,
		},
	})
	return err
}

func (d *DB) UpdateFlipbook(id, title, description string) error {
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{
		"$set": bson.M{
			"title":       title,
			"description": description,
			"updated_at":  time.Now(),
		},
	})
	return err
}

func (d *DB) DeleteFlipbook(id string) error {
	now := time.Now()
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{
		"$set": bson.M{
			"deleted_at": now,
			"updated_at": now,
		},
	})
	return err
}

func (d *DB) GetFlipbooksByStatus(status string) ([]*models.Flipbook, error) {
	filter := bson.M{"status": status, "deleted_at": nil}
	cursor, err := d.flipbooks.Find(context.Background(), filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	var results []*models.Flipbook
	for cursor.Next(context.Background()) {
		var doc flipbookDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		results = append(results, docToModel(&doc))
	}
	return results, nil
}

func (d *DB) EnsureUniqueSlug(slug string) string {
	base := slug
	counter := 1
	for {
		count, _ := d.flipbooks.CountDocuments(context.Background(), bson.M{"slug": slug})
		if count == 0 {
			return slug
		}
		counter++
		slug = fmt.Sprintf("%s-%d", base, counter)
	}
}

func (d *DB) RecordView(flipbookID, referrer, userAgent string) {
	d.views.InsertOne(context.Background(), viewDoc{
		FlipbookID: flipbookID,
		ViewedAt:   time.Now(),
		Referrer:   referrer,
		UserAgent:  userAgent,
	})
}

func (d *DB) GetViewCount(flipbookID string) int {
	count, _ := d.views.CountDocuments(context.Background(), bson.M{"flipbook_id": flipbookID})
	return int(count)
}

// FormatTime returns a human-friendly time string.
func FormatTime(t time.Time) string {
	return t.Format("Jan 2, 2006 3:04 PM")
}

// --- Settings (admin password) ---

func (d *DB) SetSetting(key, value string) error {
	_, err := d.settings.ReplaceOne(context.Background(),
		bson.M{"_id": key},
		settingDoc{Key: key, Value: value},
		options.Replace().SetUpsert(true),
	)
	return err
}

func (d *DB) GetSetting(key string) (string, error) {
	var doc settingDoc
	err := d.settings.FindOne(context.Background(), bson.M{"_id": key}).Decode(&doc)
	if err != nil {
		return "", err
	}
	return doc.Value, nil
}

// --- Sessions ---

func (d *DB) CreateSession(token string, ttl time.Duration) error {
	now := time.Now()
	_, err := d.sessions.InsertOne(context.Background(), sessionDoc{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	})
	return err
}

func (d *DB) ValidateSession(token string) bool {
	var doc sessionDoc
	err := d.sessions.FindOne(context.Background(), bson.M{"_id": token}).Decode(&doc)
	if err != nil {
		return false
	}
	return time.Now().Before(doc.ExpiresAt)
}

func (d *DB) DeleteSession(token string) error {
	_, err := d.sessions.DeleteOne(context.Background(), bson.M{"_id": token})
	return err
}

func (d *DB) CleanExpiredSessions() {
	d.sessions.DeleteMany(context.Background(), bson.M{
		"expires_at": bson.M{"$lt": time.Now()},
	})
}

// --- GridFS (original file backup) ---

func (d *DB) UploadToGridFS(ctx context.Context, filename string, r io.Reader) (string, error) {
	fileID, err := d.gridfs.UploadFromStream(ctx, filename, r)
	if err != nil {
		return "", fmt.Errorf("gridfs upload: %w", err)
	}
	return fileID.Hex(), nil
}

func (d *DB) DownloadFromGridFS(ctx context.Context, fileIDHex string, w io.Writer) (int64, error) {
	oid, err := bson.ObjectIDFromHex(fileIDHex)
	if err != nil {
		return 0, fmt.Errorf("invalid gridfs file id: %w", err)
	}
	return d.gridfs.DownloadToStream(ctx, oid, w)
}

func (d *DB) DeleteFromGridFS(ctx context.Context, fileIDHex string) error {
	oid, err := bson.ObjectIDFromHex(fileIDHex)
	if err != nil {
		return fmt.Errorf("invalid gridfs file id: %w", err)
	}
	return d.gridfs.Delete(ctx, oid)
}

func (d *DB) SetGridFSFileID(id, gridfsFileID string) error {
	_, err := d.flipbooks.UpdateByID(context.Background(), id, bson.M{
		"$set": bson.M{
			"gridfs_file_id": gridfsFileID,
			"updated_at":     time.Now(),
		},
	})
	return err
}
