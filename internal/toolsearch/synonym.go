package toolsearch

import (
	"sort"
	"strings"
)

// phraseSynonyms runs before tokenization. Chinese terms intentionally live
// here because their individual Han tokens cannot express multi-character
// terms such as "虚拟机".
var phraseSynonyms = map[string]string{
	"virtual machine": "vm qemu virtual machine",
	"pull request":    "pr pull request",
	"merge request":   "mr merge request",
	"check run":       "ci check run",
	"虚拟机":             "vm qemu",
	"拉取请求":            "pull request pr",
	"合并请求":            "merge request mr",
	"容器":              "container lxc docker",
	"仓库":              "repo repository",
	"快照":              "snapshot",
	"备份":              "backup",
	"节点":              "node",
	"存储":              "storage disk",
	"磁盘":              "disk",
	"网络":              "network",
	"权限":              "permission acl",
	"目录":              "directory folder dir",
	"文件":              "file",
	"任务":              "task job",
	"状态":              "status state",
	"配置":              "config",
	"日志":              "log",
	"用户":              "user",
	"媒体":              "media video movie",
	"监控":              "monitor metrics",
	"列出":              "list",
	"列表":              "list",
	"查看":              "get list",
	"查询":              "query search",
	"搜索":              "search query",
	"获取":              "get",
	"创建":              "create",
	"新建":              "create",
	"删除":              "delete remove",
	"更新":              "update",
	"修改":              "update",
	"启动":              "start",
	"停止":              "stop",
	"重启":              "restart",
}

var tokenSynonyms = map[string][]string{
	"pr": {"pull", "request"}, "mr": {"merge", "request"},
	"repo": {"repository"}, "repository": {"repo"},
	"vm": {"qemu", "virtual"}, "qemu": {"vm"},
	"ct": {"container", "lxc"}, "lxc": {"container"},
	"ci": {"check", "run"}, "cd": {"deploy", "deployment"},
	"cfg": {"config", "configuration"}, "conf": {"config", "configuration"},
	"db": {"database"}, "fs": {"file", "filesystem"},
	"k8s": {"kubernetes"}, "svc": {"service"}, "ns": {"namespace"},
	"del": {"delete", "remove"}, "ls": {"list"}, "img": {"image"},
	"vol": {"volume"}, "msg": {"message"}, "desc": {"description"},
}

var phraseSynonymKeys = func() []string {
	keys := make([]string, 0, len(phraseSynonyms))
	for key := range phraseSynonyms {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len([]rune(keys[i])) != len([]rune(keys[j])) {
			return len([]rune(keys[i])) > len([]rune(keys[j]))
		}
		return keys[i] < keys[j]
	})
	return keys
}()

func applyPhraseSynonyms(query string) string {
	for _, key := range phraseSynonymKeys {
		if strings.Contains(query, key) {
			query = strings.ReplaceAll(query, key, " "+phraseSynonyms[key]+" ")
		}
	}
	return normalizeQuery(query)
}
