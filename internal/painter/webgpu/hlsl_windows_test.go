package webgpu

import (
	"regexp"
	"testing"
	"unsafe"

	"github.com/roffe/gowgpu/hal/dx12/d3dcompile"
	"github.com/roffe/naga"
	"github.com/roffe/naga/hlsl"
	"github.com/roffe/naga/ir"
)

// TestShadersCompileWithFXC compiles every pipeline's WGSL the way gogpu's
// DX12 backend does: naga writes HLSL, d3dcompiler_47 compiles it. Nothing
// else reads naga's HLSL, so where naga gets a construct wrong (v0.19 writes a
// private array as "float4[12] data") every other test still passes. It runs
// under wine as well:
//
//	GOOS=windows CGO_ENABLED=0 go test -exec wine -run FXC ./internal/painter/webgpu
//
// wine's compiler has no shader model 5.1 resource arrays yet, so naga's
// sampler heap is swapped for plain samplers first; FXC takes either.
func TestShadersCompileWithFXC(t *testing.T) {
	lib, err := d3dcompile.Load()
	if err != nil {
		t.Skip("no d3dcompiler_47:", err)
	}
	srcs := map[string]string{
		"texture": shaderTexture + recordsConst(unsafe.Sizeof(texParams{})),
		"blur":    shaderBlur,
		"glyph":   shaderGlyph,
		"line":    shaderLine,
		"blit":    shaderBlit,
		"user":    shaderUserPrelude + testShaderWGSL,
	}
	for _, s := range shapeShaders {
		srcs[s.name] = shaderCommon + s.src + recordsConst(s.record)
	}
	heap := regexp.MustCompile(`(?m)^(SamplerState nagaSamplerHeap|SamplerComparisonState nagaComparisonSamplerHeap|StructuredBuffer<uint> nagaGroup\d+SamplerIndexArray).*\n`)
	heapUse := regexp.MustCompile(`static const SamplerState (\w+) = nagaSamplerHeap\[nagaGroup\d+SamplerIndexArray\[(\d+)\]\];`)

	for name, src := range srcs {
		ast, err := naga.Parse(src)
		if err != nil {
			t.Fatal(name, err)
		}
		mod, err := naga.LowerWithSource(ast, src)
		if err != nil {
			t.Fatal(name, err)
		}
		out, info, err := hlsl.Compile(mod, hlsl.DefaultOptions())
		if err != nil {
			t.Error(name, err)
			continue
		}
		out = heapUse.ReplaceAllString(heap.ReplaceAllString(out, ""), "SamplerState $1 : register(s$2);")
		for _, ep := range mod.EntryPoints {
			target := d3dcompile.TargetVS51
			if ep.Stage == ir.StageFragment {
				target = d3dcompile.TargetPS51
			}
			entry := ep.Name
			if mapped, ok := info.EntryPointNames[ep.Name]; ok {
				entry = mapped
			}
			if _, err := lib.Compile(out, entry, target); err != nil {
				t.Errorf("%s %s: %v", name, entry, err)
			}
		}
	}
}
