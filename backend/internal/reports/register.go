package reports

import (
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// Register 声明五个报表读操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[BusinessReportInput, BusinessReport]{Name: "get_business_report", Summary: "查看营业日报或月报", Kind: ops.Read, Permission: perm.ReportView, AI: ops.AIDirect, Handle: getBusinessReport})
	ops.Register(r, ops.Operation[StaffReportInput, StaffReport]{Name: "get_staff_report", Summary: "查看员工业绩分析", Kind: ops.Read, Permission: perm.PerformanceView, AI: ops.AIDirect, Handle: getStaffReport})
	ops.Register(r, ops.Operation[ItemReportInput, ItemReport]{Name: "get_item_report", Summary: "查看项目分析", Kind: ops.Read, Permission: perm.ReportView, AI: ops.AIDirect, Handle: getItemReport})
	ops.Register(r, ops.Operation[CardReportInput, CardReport]{Name: "get_card_report", Summary: "查看卡项分析", Kind: ops.Read, Permission: perm.ReportView, AI: ops.AIDirect, Handle: getCardReport})
	ops.Register(r, ops.Operation[CustomerSourceReportInput, CustomerSourceReport]{Name: "get_customer_source_report", Summary: "查看新客来源报表", Kind: ops.Read, Permission: perm.ReportView, AI: ops.AIDirect, Handle: getCustomerSourceReport})
}
