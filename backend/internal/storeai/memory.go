package storeai

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

const (
	// MaxMemoryFileBytes 是一个 AI 记忆文件允许的最大字节数。
	MaxMemoryFileBytes = 16384
	// MaxMemoryFiles 是一个收件箱允许的记忆文件数。
	MaxMemoryFiles = 200
)

var memoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// MemoryFile 是 AI 记忆文件列表里的一项，只带路径、字节数和更新时间。
type MemoryFile struct {
	Path      string    `json:"path"`
	Bytes     int       `json:"bytes"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AlwaysMemory 是每次运行都要加载的三个常驻文件；缺的文件按空字符串处理。
type AlwaysMemory struct {
	Instructions string
	Index        string
	Reported     string
}

// ReadMemory 读一个记忆文件；文件不存在时返回空字符串。
func ReadMemory(ctx context.Context, q *dbgen.Queries, inboxID uuid.UUID, path string) (string, error) {
	if !validMemoryPath(path) {
		return "", ErrInvalidMemoryPath
	}
	file, err := q.GetMemoryFile(ctx, dbgen.GetMemoryFileParams{InboxID: inboxID, Path: path})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return file.Content, nil
}

// WriteMemory 写入或覆盖一个记忆文件。写新文件前先锁住收件箱行再数，避免并发写超出 200 个。
// 调用方应在同一个租户事务里调用它。
func WriteMemory(ctx context.Context, q *dbgen.Queries, now time.Time, inboxID uuid.UUID, path, content string) error {
	if !validMemoryPath(path) {
		return ErrInvalidMemoryPath
	}
	if byteLen(content) > MaxMemoryFileBytes {
		return ErrMemoryTooLarge
	}
	inbox, err := q.LockInbox(ctx, inboxID)
	if err != nil {
		return err
	}
	_, err = q.GetMemoryFile(ctx, dbgen.GetMemoryFileParams{InboxID: inboxID, Path: path})
	if errors.Is(err, pgx.ErrNoRows) {
		count, err := q.CountMemoryFiles(ctx, inboxID)
		if err != nil {
			return err
		}
		if count >= MaxMemoryFiles {
			return ErrMemoryLimit
		}
	} else if err != nil {
		return err
	}
	_, err = q.UpsertMemoryFile(ctx, dbgen.UpsertMemoryFileParams{
		TenantID: inbox.TenantID, StoreID: inbox.StoreID, InboxID: inboxID,
		Path: path, Content: content, UpdatedAt: timestamp(now),
	})
	return err
}

// DeleteMemory 删除一个记忆文件；文件不存在不算错误。
func DeleteMemory(ctx context.Context, q *dbgen.Queries, inboxID uuid.UUID, path string) error {
	if !validMemoryPath(path) {
		return ErrInvalidMemoryPath
	}
	return q.DeleteMemoryFile(ctx, dbgen.DeleteMemoryFileParams{InboxID: inboxID, Path: path})
}

// ListMemory 列出一个收件箱的记忆文件，只返回路径、字节数和更新时间。
func ListMemory(ctx context.Context, q *dbgen.Queries, inboxID uuid.UUID) ([]MemoryFile, error) {
	rows, err := q.ListMemoryFiles(ctx, inboxID)
	if err != nil {
		return nil, err
	}
	files := make([]MemoryFile, 0, len(rows))
	for _, row := range rows {
		files = append(files, MemoryFile{Path: row.Path, Bytes: int(row.Bytes), UpdatedAt: row.UpdatedAt.Time})
	}
	return files, nil
}

// LoadAlwaysMemory 加载 instructions.md、index.md、reported.md；缺的按空字符串处理。
func LoadAlwaysMemory(ctx context.Context, q *dbgen.Queries, inboxID uuid.UUID) (AlwaysMemory, error) {
	rows, err := q.ListAlwaysMemoryFiles(ctx, inboxID)
	if err != nil {
		return AlwaysMemory{}, err
	}
	var out AlwaysMemory
	for _, row := range rows {
		switch row.Path {
		case "instructions.md":
			out.Instructions = row.Content
		case "index.md":
			out.Index = row.Content
		case "reported.md":
			out.Reported = row.Content
		}
	}
	return out, nil
}

func validMemoryPath(path string) bool {
	switch path {
	case "instructions.md", "index.md", "reported.md":
		return true
	}
	for _, prefix := range []string{"notes/", "progress/"} {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok || !strings.HasSuffix(rest, ".md") {
			continue
		}
		name := strings.TrimSuffix(rest, ".md")
		if memoryName.MatchString(name) {
			return true
		}
	}
	return false
}
