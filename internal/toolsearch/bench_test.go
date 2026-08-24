package toolsearch

import (
	"fmt"
	"testing"
)

func benchmarkDocs(count int) []Doc {
	docs := make([]Doc, 0, count)
	for i := range count {
		docs = append(docs, Doc{
			Name:          fmt.Sprintf("pve_vm_snapshot_create_%03d", i),
			OriginalName:  fmt.Sprintf("create_qemu_snapshot_%03d", i),
			Description:   "Create a virtual machine snapshot with a descriptive but bounded tool explanation.",
			UpstreamNames: []string{"PVE Production"},
			UpstreamTags:  []string{"virtualization", "operations"},
		})
	}
	return docs
}

func BenchmarkBuild500(b *testing.B) {
	docs := benchmarkDocs(500)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = Build(docs)
	}
}

func BenchmarkSearch500(b *testing.B) {
	ix := Build(benchmarkDocs(500))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = ix.Search("pve create vm snapshot", 50, 0)
	}
}
