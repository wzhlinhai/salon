package drafts_test

func confirmArgs(d map[string]any) map[string]any {
	return map[string]any{"id": d["id"], "version": d["version"]}
}
