package dataio

import (
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
)

// Register 声明导入批次的提交、撤销和查询操作。
func Register(r *ops.Registry) {
	ops.Register(r, ops.Operation[CommitImportInput, ImportBatchDetail]{Name: "commit_import", Summary: "提交预览通过的导入批次", Kind: ops.Write, Permission: perm.DataImport, AI: ops.AINone, Handle: commit})
	ops.Register(r, ops.Operation[UndoImportInput, ImportBatchDetail]{Name: "undo_import", Summary: "撤销一批已提交的导入", Kind: ops.Write, Permission: perm.DataImport, AI: ops.AINone, Handle: undo})
	ops.Register(r, ops.Operation[ListImportBatchesInput, ImportBatchList]{Name: "list_import_batches", Summary: "列出导入批次及核对差额", Kind: ops.Read, Permission: perm.DataImport, AI: ops.AIDirect, Handle: listBatches})
	ops.Register(r, ops.Operation[GetImportBatchInput, ImportBatchDetail]{Name: "get_import_batch", Summary: "查看一批导入的行结果与核对差额", Kind: ops.Read, Permission: perm.DataImport, AI: ops.AIDirect, Handle: getBatch})
}
