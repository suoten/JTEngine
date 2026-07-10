package module

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-2]: 模块依赖图与循环依赖检测
//
// ModuleLoader 启动时构建依赖图，使用 Kahn 算法进行拓扑排序：
//   1. 收集每个模块的依赖（DependentModule.Depends()）
//   2. 检测缺失依赖（依赖的模块未加载）→ 拒绝加载并告警
//   3. 检测循环依赖（拓扑排序后未覆盖全部模块）→ 拒绝加载并告警
//   4. 同层内按 Priority 升序稳定排序（核心→存储→协议→业务→运维）
//
// 加载顺序由 topologicalOrder 返回：InitAll/StartAll 正序，StopAll 逆序。
// ===================================================================

import (
	"fmt"
	"sort"
	"strings"
)

// depGraph 模块依赖图。
// nodes: 模块名 → 入度（依赖的其他模块数）
// edges: 模块名 → 依赖它的模块列表（邻接表，u→v 表示 v 依赖 u）
type depGraph struct {
	nodes  map[string]int      // 入度
	edges  map[string][]string // 邻接表（被依赖方 → 依赖方列表）
	names  []string            // 所有节点名（保留插入顺序用于稳定排序）
	exists map[string]bool     // 节点存在标记
}

// buildDepGraph 从已加载模块构建依赖图。
// 对未实现 DependentModule 的模块视为无依赖（入度为 0）。
// 缺失依赖（依赖了未加载的模块）记录到 missingDeps 返回。
func (l *Loader) buildDepGraph() (g *depGraph, missingDeps map[string][]string) {
	g = &depGraph{
		nodes:  make(map[string]int),
		edges:  make(map[string][]string),
		exists: make(map[string]bool),
	}
	missingDeps = make(map[string][]string)

	// 第一遍：注册所有节点
	for name := range l.modules {
		g.nodes[name] = 0
		g.exists[name] = true
		g.names = append(g.names, name)
	}

	// 第二遍：收集依赖，构建邻接表与入度
	for name, lm := range l.modules {
		var deps []string
		if dep, ok := lm.Module.(DependentModule); ok {
			deps = dep.Depends()
		}
		for _, dep := range deps {
			dep = strings.TrimSpace(dep)
			if dep == "" || dep == name {
				continue
			}
			if !g.exists[dep] {
				// 依赖的模块未加载
				missingDeps[name] = append(missingDeps[name], dep)
				continue
			}
			// 边：dep → name（name 依赖 dep）
			g.edges[dep] = append(g.edges[dep], name)
			g.nodes[name]++
		}
	}

	// 保持 names 稳定排序（便于同层内确定性输出）
	sort.Strings(g.names)
	return g, missingDeps
}

// topologicalOrder 执行 Kahn 算法拓扑排序。
// 返回排序后的模块名列表（先加载在前）。
// 同层（同时入度为 0）的节点按 Priority 升序、名称字母序稳定排序。
// 若存在环（排序结果少于节点总数），返回 (order, cycleNodes)。
func (g *depGraph) topologicalOrder() (order []string, cycle []string) {
	inDegree := make(map[string]int, len(g.nodes))
	for k, v := range g.nodes {
		inDegree[k] = v
	}

	// 初始队列：入度为 0 的节点
	var queue []string
	for _, name := range g.names {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	// 队列内稳定排序：先按 Priority，再按名称
	sortQueue := func(q []string) {
		sort.SliceStable(q, func(i, j int) bool {
			pi := priorityOf(q[i])
			pj := priorityOf(q[j])
			if pi != pj {
				return pi < pj
			}
			return q[i] < q[j]
		})
	}

	order = make([]string, 0, len(g.nodes))
	for len(queue) > 0 {
		sortQueue(queue)
		// 取出队首
		cur := queue[0]
		queue = queue[1:]
		order = append(order, cur)

		// 遍历 cur 指向的节点，入度 -1，若归零则入队
		// 邻接表需稳定排序保证确定性
		nexts := g.edges[cur]
		sort.Strings(nexts)
		for _, next := range nexts {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(order) < len(g.nodes) {
		// 存在环：收集所有仍在环中的节点（入度 > 0）
		inCycle := make(map[string]bool)
		for name, deg := range inDegree {
			if deg > 0 {
				inCycle[name] = true
			}
		}
		for name := range g.nodes {
			if inCycle[name] {
				cycle = append(cycle, name)
			}
		}
		sort.Strings(cycle)
		return order, cycle
	}

	return order, nil
}

// priorityOf 包级辅助函数：获取模块优先级。
// depGraph 在拓扑排序阶段无法访问 Loader.modules，因此仅按名称推断。
// 优先级分层：核心(10) → 存储(20) → 协议(30) → 业务(40) → 运维(50)。
// 若需精确控制优先级，模块应实现 PrioritizedModule 接口（在 Loader 内另行处理）。
func priorityOf(name string) int {
	return classifyPriority(name)
}

// formatCycle 格式化循环依赖告警信息。
func formatCycle(cycle []string) string {
	return fmt.Sprintf("circular dependency detected among modules: %v (loading aborted)", cycle)
}

// formatMissingDeps 格式化缺失依赖告警信息。
func formatMissingDeps(missing map[string][]string) string {
	var sb strings.Builder
	sb.WriteString("modules have missing dependencies (loading skipped):")
	for name, deps := range missing {
		sb.WriteString(fmt.Sprintf("\n  - %s depends on unloaded: %v", name, deps))
	}
	return sb.String()
}
