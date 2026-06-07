# MCP Proxy Gateway 管理前端

MCP Proxy Gateway 的前端管理界面，基于 [TailAdmin Vue](https://github.com/TailAdmin/vue-tailwind-admin-dashboard) 模板适配改造。

## 技术栈

- Vue 3.x + Vite
- TypeScript
- Tailwind CSS 4.x
- vue-router / pinia
- ESLint + Prettier（含 prettier-plugin-tailwindcss 工具类排序）

## 常用脚本

```bash
npm install        # 安装依赖
npm run dev        # 启动开发服务器
npm run build      # 类型检查 + 生产构建
npm run type-check # 仅类型检查
npm run lint       # ESLint 校验（发现问题以非零退出码报告）
npm run lint:fix   # ESLint 自动修复
npm run format     # Prettier 格式化 src/
npm run format:check # Prettier 格式校验
```

## 目录约定

- `src/components/layout/` 基础布局（侧边栏、顶栏、主题/侧栏 Provider）
- `src/views/` 页面视图
- `src/router/` 路由配置
- `src/composables/` 组合式函数
- `src/icons/` 图标组件
