// Package storetime 保存门店所在时区，供数据库和业务包共用，避免依赖 settings。
package storetime

import (
	"time"
	_ "time/tzdata" // 部署环境可能没有时区数据库。
)

// Location 是门店所在时区。P0 只服务中国的门店。
var Location = mustLoadLocation("Asia/Shanghai")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}
