package risk

import "testing"

func TestDeterministicAssessment(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        Level
	}{
		{"delete_file", "永久删除指定文件", LevelHigh},
		{"get_deleted_items", "列出回收站中已删除的项目，不会修改数据", LevelLow},
		{"refresh_metadata", "刷新媒体元数据", LevelMedium},
		{"update_acl", "修改访问控制权限", LevelHigh},
		{"execute_shell", "执行任意 shell 命令", LevelBlocked},
		{"clear_recycle_bin", "永久清空回收站", LevelBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeterministicAssessment(tt.name, tt.description)
			if got.Floor != tt.want {
				t.Fatalf("floor = %q, want %q (tags=%v)", got.Floor, tt.want, got.Tags)
			}
		})
	}
}
