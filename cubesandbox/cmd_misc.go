package main

import (
	"fmt"
	"strconv"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// ---------------- 快照 / 回滚 / 克隆 ----------------

func cmdSnap(c *cubesandbox.Client, args []string) {
	flags, sid, _ := splitArgs(args)
	sb := connect(c, sid)
	info, err := sb.CreateSnapshot(ctx, flags["name"])
	if err != nil {
		fatal("创建快照失败: %v", err)
	}
	printJSON(info)
}

func cmdSnapLs(c *cubesandbox.Client, args []string) {
	flags, _, _ := splitArgs(args)
	opts := cubesandbox.ListSnapshotsOptions{SandboxID: flags["sandbox"]}
	if v := flags["limit"]; v != "" {
		n, _ := strconv.Atoi(v)
		opts.Limit = n
	}
	list, next, err := c.ListSnapshots(ctx, opts)
	if err != nil {
		fatal("列出快照失败: %v", err)
	}
	printJSON(map[string]any{"snapshots": list, "nextToken": next})
}

func cmdSnapRm(c *cubesandbox.Client, args []string) {
	_, sid, _ := splitArgs(args)
	need(sid != "", "用法: snap-rm <snapID>")
	if err := c.DeleteSnapshot(ctx, sid); err != nil {
		fatal("删除快照失败: %v", err)
	}
	fmt.Println("deleted snapshot", sid)
}

func cmdRollback(c *cubesandbox.Client, args []string) {
	_, sid, rest := splitArgs(args)
	need(len(rest) >= 1, "用法: rollback <sid> <snapID>")
	sb := connect(c, sid)
	res, err := sb.Rollback(ctx, rest[0])
	if err != nil {
		fatal("回滚失败: %v", err)
	}
	printJSON(res)
}

func cmdClone(c *cubesandbox.Client, args []string) {
	flags, sid, _ := splitArgs(args)
	n := 1
	if v := flags["n"]; v != "" {
		n, _ = strconv.Atoi(v)
	}
	conc := 0
	if v := flags["concurrency"]; v != "" {
		conc, _ = strconv.Atoi(v)
	}
	sb := connect(c, sid)
	list, err := sb.Clone(ctx, cubesandbox.CloneOptions{N: n, Concurrency: conc})
	if err != nil {
		fatal("克隆失败: %v", err)
	}
	for _, s := range list {
		fmt.Println(s.SandboxID)
	}
}

// ---------------- 持久卷 ----------------

func cmdVolLs(c *cubesandbox.Client, args []string) {
	flags, _, _ := splitArgs(args)
	list, err := c.ListVolumes(ctx)
	if err != nil {
		fatal("列出卷失败: %v", err)
	}
	if flags["json"] == "true" {
		printJSON(list)
		return
	}
	if len(list) == 0 {
		fmt.Println("(无卷)")
		return
	}
	for _, v := range list {
		fmt.Printf("%-32s %s\n", v.VolumeID, v.Name)
	}
}

func cmdVolNew(c *cubesandbox.Client, args []string) {
	flags, name, _ := splitArgs(args)
	need(name != "", "用法: vol-new <名字> [--driver=插件名]")
	v, err := c.CreateVolume(ctx, cubesandbox.CreateVolumeOptions{Name: name, Driver: flags["driver"]})
	if err != nil {
		fatal("创建卷失败: %v", err)
	}
	printJSON(v)
}

func cmdVolInfo(c *cubesandbox.Client, args []string) {
	_, vid, _ := splitArgs(args)
	need(vid != "", "用法: vol-info <卷ID>")
	v, err := c.GetVolume(ctx, vid)
	if err != nil {
		fatal("查询卷失败: %v", err)
	}
	printJSON(v)
}

func cmdVolRm(c *cubesandbox.Client, args []string) {
	_, vid, _ := splitArgs(args)
	need(vid != "", "用法: vol-rm <卷ID>（需先销毁所有挂载它的沙箱）")
	if err := c.DeleteVolume(ctx, vid); err != nil {
		fatal("删除卷失败: %v", err)
	}
	fmt.Println("deleted volume", vid)
}

// ---------------- 模板 / 健康 ----------------

func cmdTplInfo(c *cubesandbox.Client, args []string) {
	_, tid, _ := splitArgs(args)
	need(tid != "", "用法: tpl-info <模板ID>")
	t, err := c.GetTemplate(ctx, tid)
	if err != nil {
		fatal("查询模板失败: %v", err)
	}
	printJSON(t)
}

func cmdHealth(c *cubesandbox.Client, args []string) {
	h, err := c.Health(ctx)
	if err != nil {
		fatal("健康检查失败: %v", err)
	}
	printJSON(h)
}
