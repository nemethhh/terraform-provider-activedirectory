package provider_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nemethhh/go-adcore"
	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
)

// provisionLargeGroup builds a large-set fixture and registers its teardown.
// PowerShell connections provision it in one pwsh pass; the ldap connection has
// no PowerShell, so it provisions through the directory the suite verifies with.
func provisionLargeGroup(t *testing.T, payload map[string]any) largeGroupResult {
	t.Helper()
	if accTransportName() == "ldap" {
		return provisionLargeOverDirectory(t, accClient(t), payload)
	}

	tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath), Timeout: 30 * time.Minute})
	if err != nil {
		t.Fatalf("start PowerShell: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	prov, err := runLargeGroup(context.Background(), tr, payload)
	if err != nil {
		t.Fatalf("%s %v members: %v", payload["action"], payload["count"], err)
	}
	t.Cleanup(func() {
		if _, err := runLargeGroup(context.Background(), tr, map[string]any{
			"action": "teardown", "ou": prov.OU,
		}); err != nil {
			t.Errorf("teardown %s: %v", prov.OU, err)
		}
	})
	return prov
}

func largeGroupReader(t *testing.T) adcore.Directory {
	t.Helper()
	if accTransportName() == "ldap" {
		return accClient(t)
	}
	tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath), Timeout: 15 * time.Minute})
	if err != nil {
		t.Fatalf("start PowerShell: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	cfg := adpwsh.Config{Transport: tr, Server: os.Getenv(envServer)}
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		cfg.Credential = &adpwsh.Credential{Username: u, Password: adpwsh.NewSecret(p)}
	}
	client, err := adpwsh.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("configure client: %v", err)
	}
	return client.Directory()
}

func provisionLargeOverDirectory(t *testing.T, dir adcore.Directory, payload map[string]any) largeGroupResult {
	t.Helper()
	ctx := context.Background()
	base, tag := payload["base"].(string), payload["tag"].(string)
	count := payload["count"].(int)

	ou, err := dir.OU.Create(ctx, adcore.OUSpec{Name: tag, Container: base, Protected: adcore.Bool(false)})
	if err != nil {
		t.Fatalf("create fixture OU: %v", err)
	}
	var groups, users []string
	var mu sync.Mutex
	t.Cleanup(func() {
		ctx := context.Background()
		for _, g := range groups {
			if err := dir.Group.Delete(ctx, adcore.ByGUID(g)); err != nil {
				t.Errorf("teardown group %s: %v", g, err)
			}
		}
		if err := parallelLarge(len(users), func(i int) error {
			if users[i] == "" {
				return nil
			}
			return dir.User.Delete(ctx, adcore.ByGUID(users[i]))
		}); err != nil {
			t.Errorf("teardown users: %v", err)
		}
		if err := dir.OU.Delete(ctx, adcore.ByGUID(ou.GUID), adcore.DeleteOptions{Unprotect: true}); err != nil {
			t.Errorf("teardown %s: %v", ou.DN, err)
		}
	})

	group := func(name string) string {
		g, err := dir.Group.Create(ctx, adcore.GroupSpec{
			Name: name, SamAccountName: name, Container: ou.DN,
			Scope: adcore.GroupScopeGlobal, Category: adcore.GroupCategorySecurity,
		})
		if err != nil {
			t.Fatalf("create group %s: %v", name, err)
		}
		groups = append([]string{g.GUID}, groups...)
		return g.GUID
	}
	addMembers := func(groupGUID string, members []string) {
		for i := 0; i < len(members); i += 500 {
			ids := make([]adcore.Identity, 0, 500)
			for _, m := range members[i:min(i+500, len(members))] {
				ids = append(ids, adcore.ByGUID(m))
			}
			if err := dir.Group.AddMembers(ctx, adcore.ByGUID(groupGUID), ids); err != nil {
				t.Fatalf("add members to %s: %v", groupGUID, err)
			}
		}
	}

	users = make([]string, count)
	if err := parallelLarge(count, func(i int) error {
		name := fmt.Sprintf("%s-m%d", tag, i)
		u, err := dir.User.Create(ctx, adcore.UserSpec{
			SamAccountName: name, Name: adcore.String(name), Container: ou.DN, Enabled: adcore.Bool(false),
		})
		if err != nil {
			return fmt.Errorf("create user %s: %w", name, err)
		}
		mu.Lock()
		users[i] = u.GUID
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res := largeGroupResult{OU: ou.DN, Count: count}
	switch payload["action"] {
	case "provision":
		res.GroupGUID = group(tag + "-grp")
		addMembers(res.GroupGUID, users)
	case "provision_nested":
		buckets := payload["buckets"].(int)
		res.Buckets = buckets
		res.TopGUID = group(tag + "-top")
		res.FlatGUID = group(tag + "-flat")
		start := 0
		for b := 0; b < buckets; b++ {
			share := count / buckets
			if b < count%buckets {
				share++
			}
			child := group(fmt.Sprintf("%s-c%d", tag, b))
			addMembers(child, users[start:start+share])
			addMembers(res.TopGUID, []string{child})
			start += share
		}
		addMembers(res.FlatGUID, users)
	default:
		t.Fatalf("unknown action %v", payload["action"])
	}
	return res
}

func parallelLarge(n int, f func(int) error) error {
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	work := make(chan int)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				if err := f(i); err != nil {
					once.Do(func() { first = err })
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		work <- i
	}
	close(work)
	wg.Wait()
	return first
}
