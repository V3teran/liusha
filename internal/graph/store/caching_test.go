package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	graphmodel "github.com/V3teran/liusha/internal/graph/model"
	"github.com/V3teran/liusha/internal/graph/store"
)

type fakeCold struct {
	nodes   map[uuid.UUID]graphmodel.Node
	addErr  error
	getErr  error
	addCall int
	getCall int
}

func newFakeCold() *fakeCold {
	return &fakeCold{nodes: map[uuid.UUID]graphmodel.Node{}}
}

func (f *fakeCold) AddNode(_ context.Context, n graphmodel.Node) error {
	f.addCall++
	if f.addErr != nil {
		return f.addErr
	}
	f.nodes[n.ID] = n
	return nil
}

func (f *fakeCold) GetNode(_ context.Context, id uuid.UUID) (graphmodel.Node, error) {
	f.getCall++
	if f.getErr != nil {
		return graphmodel.Node{}, f.getErr
	}
	n, ok := f.nodes[id]
	if !ok {
		return graphmodel.Node{}, errors.New("not found")
	}
	return n, nil
}

type fakeHot struct {
	nodes   map[uuid.UUID]graphmodel.Node
	setErr  error
	getErr  error // 非 nil 视为 miss/错误
	setCall int
	getCall int
}

func newFakeHot() *fakeHot {
	return &fakeHot{nodes: map[uuid.UUID]graphmodel.Node{}}
}

func (f *fakeHot) SetNode(_ context.Context, n graphmodel.Node) error {
	f.setCall++
	if f.setErr != nil {
		return f.setErr
	}
	f.nodes[n.ID] = n
	return nil
}

func (f *fakeHot) GetNode(_ context.Context, _, nodeID uuid.UUID) (graphmodel.Node, error) {
	f.getCall++
	if f.getErr != nil {
		return graphmodel.Node{}, f.getErr
	}
	n, ok := f.nodes[nodeID]
	if !ok {
		return graphmodel.Node{}, errors.New("miss")
	}
	return n, nil
}

func mkNode() graphmodel.Node {
	return graphmodel.Node{
		ID:           uuid.New(),
		EngagementID: uuid.New(),
		Kind:         graphmodel.KindFinding,
		Payload:      map[string]any{"title": "t"},
	}
}

func TestCachingStore_AddNode_WritesBoth(t *testing.T) {
	cold, hot := newFakeCold(), newFakeHot()
	s := store.NewCachingStore(cold, hot, nil)

	n := mkNode()
	require.NoError(t, s.AddNode(context.Background(), n))

	assert.Equal(t, 1, cold.addCall)
	assert.Equal(t, 1, hot.setCall)
	_, ok := hot.nodes[n.ID]
	assert.True(t, ok, "热缓存应落入节点")
}

func TestCachingStore_AddNode_ColdFailure_Propagates(t *testing.T) {
	cold, hot := newFakeCold(), newFakeHot()
	cold.addErr = errors.New("pg down")
	s := store.NewCachingStore(cold, hot, nil)

	err := s.AddNode(context.Background(), mkNode())
	require.Error(t, err)
	assert.Equal(t, 0, hot.setCall, "冷存储失败时不应写热缓存")
}

func TestCachingStore_AddNode_HotFailure_DoesNotFail(t *testing.T) {
	cold, hot := newFakeCold(), newFakeHot()
	hot.setErr = errors.New("redis down")
	s := store.NewCachingStore(cold, hot, nil)

	err := s.AddNode(context.Background(), mkNode())
	require.NoError(t, err, "热缓存失败只打日志，不应向上返回")
	assert.Equal(t, 1, cold.addCall)
}

func TestCachingStore_GetNode_HotHit_SkipsCold(t *testing.T) {
	cold, hot := newFakeCold(), newFakeHot()
	s := store.NewCachingStore(cold, hot, nil)

	n := mkNode()
	hot.nodes[n.ID] = n

	got, err := s.GetNode(context.Background(), n.EngagementID, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)
	assert.Equal(t, 0, cold.getCall, "热命中时不应访问冷存储")
}

func TestCachingStore_GetNode_HotMiss_FallsBackAndBackfills(t *testing.T) {
	cold, hot := newFakeCold(), newFakeHot()
	s := store.NewCachingStore(cold, hot, nil)

	n := mkNode()
	cold.nodes[n.ID] = n

	got, err := s.GetNode(context.Background(), n.EngagementID, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)
	assert.Equal(t, 1, cold.getCall)
	assert.Equal(t, 1, hot.setCall, "miss 后应回填热缓存")
	_, ok := hot.nodes[n.ID]
	assert.True(t, ok)
}

func TestCachingStore_GetNode_HotErrorStillFallsBack(t *testing.T) {
	cold, hot := newFakeCold(), newFakeHot()
	hot.getErr = errors.New("redis timeout")
	hot.setErr = errors.New("redis down") // 回填也失败
	s := store.NewCachingStore(cold, hot, nil)

	n := mkNode()
	cold.nodes[n.ID] = n

	got, err := s.GetNode(context.Background(), n.EngagementID, n.ID)
	require.NoError(t, err, "热缓存故障不应阻断读路径")
	assert.Equal(t, n.ID, got.ID)
	assert.Equal(t, 1, cold.getCall)
}

func TestCachingStore_GetNode_ColdMiss_ReturnsError(t *testing.T) {
	cold, hot := newFakeCold(), newFakeHot()
	s := store.NewCachingStore(cold, hot, nil)

	_, err := s.GetNode(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}
