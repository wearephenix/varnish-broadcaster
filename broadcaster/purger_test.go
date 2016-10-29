package broadcasters

import (
	"fmt"
	"github.com/mariusmagureanu/purger/dao"
	"testing"
)

func BenchmarkDoPurge(b *testing.B) {
	b.ResetTimer()
	var cache = dao.Cache{Name: "Cache 1", Host: "127.0.0.1", Port: "6081"}

	for i := 0; i < b.N; i++ {
		fmt.Println("Do something here...")
	}
}
