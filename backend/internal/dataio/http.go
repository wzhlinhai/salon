package dataio

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

const (
	// UploadPath 是预览导入的上传地址。
	UploadPath = "/api/import/preview"
	// ExportPath 是全量导出地址。
	ExportPath = "/api/export/all.xlsx"
)

// RegisterHTTP 注册导入预览上传和全量导出两个普通 HTTP 接口。
func RegisterHTTP(e *echo.Echo, pool *pgxpool.Pool, now func() time.Time) {
	e.POST(UploadPath, uploadHandler(pool, now))
	e.GET(ExportPath, exportHandler(pool, now))
}

func uploadHandler(pool *pgxpool.Pool, now func() time.Time) echo.HandlerFunc {
	return func(c *echo.Context) error {
		caller, ok := ops.CallerFrom(c.Request().Context())
		if !ok {
			return detail(c, http.StatusUnauthorized, "unauthenticated")
		}
		req := c.Request()
		req.Body = http.MaxBytesReader(c.Response(), req.Body, MaxUploadBytes+1<<20)
		if err := req.ParseMultipartForm(MaxUploadBytes + 1<<20); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return detail(c, http.StatusUnprocessableEntity, "文件超过大小上限")
			}
			return detail(c, http.StatusUnprocessableEntity, "无法读取上传文件")
		}
		file, header, err := req.FormFile("file")
		if err != nil {
			return detail(c, http.StatusUnprocessableEntity, "请选择要导入的 Excel 文件")
		}
		defer func() { _ = file.Close() }()
		if header.Size > MaxUploadBytes {
			return detail(c, http.StatusUnprocessableEntity, "文件超过大小上限")
		}
		out, err := Preview(req.Context(), pool, caller, now(), header.Filename, file)
		if err != nil {
			return mapError(c, err)
		}
		return c.JSON(http.StatusOK, out)
	}
}

func exportHandler(pool *pgxpool.Pool, now func() time.Time) echo.HandlerFunc {
	return func(c *echo.Context) error {
		caller, ok := ops.CallerFrom(c.Request().Context())
		if !ok {
			return detail(c, http.StatusUnauthorized, "unauthenticated")
		}
		// 只有真正开始写附件时才设置下载头；权限检查或查询失败仍返回 JSON，不带附件头。
		writer := &lazyHeaderWriter{w: c.Response(), set: func() {
			c.Response().Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
			c.Response().Header().Set("Content-Disposition", `attachment; filename="salon-export.xlsx"`)
		}}
		if err := Export(c.Request().Context(), writer, pool, caller, now()); err != nil {
			if writer.wrote {
				return err
			}
			c.Response().Header().Del("Content-Type")
			c.Response().Header().Del("Content-Disposition")
			return mapError(c, err)
		}
		return nil
	}
}

type lazyHeaderWriter struct {
	w     io.Writer
	set   func()
	wrote bool
}

func (l *lazyHeaderWriter) Write(p []byte) (int, error) {
	if !l.wrote {
		l.wrote = true
		l.set()
	}
	return l.w.Write(p)
}

func mapError(c *echo.Context, err error) error {
	var fileErr *fileError
	switch {
	case errors.As(err, &fileErr):
		return detail(c, http.StatusUnprocessableEntity, fileErr.Error())
	case errors.Is(err, ops.ErrForbidden):
		return detail(c, http.StatusForbidden, "permission denied")
	case errors.Is(err, ops.ErrUnauthenticated):
		return detail(c, http.StatusUnauthorized, "unauthenticated")
	default:
		return detail(c, http.StatusInternalServerError, "internal error")
	}
}

func detail(c *echo.Context, status int, message string) error {
	return c.JSON(status, map[string]string{"detail": message})
}
