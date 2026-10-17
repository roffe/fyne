package tutorials

import (
	"fyne.io/fyne/v2/canvas"
)

// cubeShaderSource renders a spinning 3D cube by ray marching a box signed
// distance field. It targets desktop OpenGL (core profile, GLSL 110).
const cubeShaderSource = `#version 110

uniform vec2 frame;
uniform vec4 bounds; //x1 [0], y1 [1], x2 [2], y2 [3]; coords in the frame
uniform float time;

mat3 rotX(float a) {
    float c = cos(a);
    float s = sin(a);
    return mat3(1.0, 0.0, 0.0, 0.0, c, s, 0.0, -s, c);
}

mat3 rotY(float a) {
    float c = cos(a);
    float s = sin(a);
    return mat3(c, 0.0, -s, 0.0, 1.0, 0.0, s, 0.0, c);
}

float sdBox(vec3 p, vec3 b) {
    vec3 d = abs(p) - b;
    return length(max(d, 0.0)) + min(max(d.x, max(d.y, d.z)), 0.0);
}

float map(vec3 p) {
    p = rotY(time) * rotX(time * 0.7) * p;
    return sdBox(p, vec3(0.6));
}

vec3 surfaceNormal(vec3 p) {
    vec2 e = vec2(0.001, 0.0);
    return normalize(vec3(
        map(p + e.xyy) - map(p - e.xyy),
        map(p + e.yxy) - map(p - e.yxy),
        map(p + e.yyx) - map(p - e.yyx)));
}

// faceColor returns a fixed colour per cube face by transforming the hit point
// into the cube's local (unrotated) space and picking the dominant axis.
vec3 faceColor(vec3 p) {
    vec3 lp = rotY(time) * rotX(time * 0.7) * p;
    vec3 a = abs(lp);
    if (a.x >= a.y && a.x >= a.z) {
        return lp.x > 0.0 ? vec3(0.90, 0.22, 0.25) : vec3(0.20, 0.70, 0.70);
    } else if (a.y >= a.z) {
        return lp.y > 0.0 ? vec3(0.32, 0.74, 0.36) : vec3(0.82, 0.32, 0.70);
    }
    return lp.z > 0.0 ? vec3(0.22, 0.46, 0.86) : vec3(0.95, 0.80, 0.26);
}

void main() {
    vec2 size = vec2(bounds[2] - bounds[0], bounds[3] - bounds[1]);
    vec2 center = vec2((bounds[0] + bounds[2]) * 0.5, frame.y - (bounds[1] + bounds[3]) * 0.5);
    vec2 uv = (gl_FragCoord.xy - center) / (0.5 * min(size.x, size.y));

    vec3 ro = vec3(0.0, 0.0, -3.0);
    vec3 rd = normalize(vec3(uv, 2.0));

    float t = 0.0;
    bool hit = false;
    vec3 p = ro;
    for (int i = 0; i < 64; i++) {
        p = ro + rd * t;
        float d = map(p);
        if (d < 0.001) {
            hit = true;
            break;
        }
        t += d;
        if (t > 6.0) {
            break;
        }
    }

    if (!hit) {
        discard;
    }

    vec3 n = surfaceNormal(p);
    vec3 lightDir = normalize(vec3(0.6, 0.8, -0.6));
    float diff = max(dot(n, lightDir), 0.0);
    vec3 col = faceColor(p) * (0.35 + 0.65 * diff);
    gl_FragColor = vec4(col, 1.0);
}
`

// cubeShaderSourceES is the OpenGL ES / mobile / web variant of cubeShaderSource.
const cubeShaderSourceES = `#version 100

#ifdef GL_ES
# ifdef GL_FRAGMENT_PRECISION_HIGH
precision highp float;
# else
precision mediump float;
#endif
precision mediump int;
#endif

uniform vec2 frame;
uniform vec4 bounds; //x1 [0], y1 [1], x2 [2], y2 [3]; coords in the frame
uniform float time;

mat3 rotX(float a) {
    float c = cos(a);
    float s = sin(a);
    return mat3(1.0, 0.0, 0.0, 0.0, c, s, 0.0, -s, c);
}

mat3 rotY(float a) {
    float c = cos(a);
    float s = sin(a);
    return mat3(c, 0.0, -s, 0.0, 1.0, 0.0, s, 0.0, c);
}

float sdBox(vec3 p, vec3 b) {
    vec3 d = abs(p) - b;
    return length(max(d, 0.0)) + min(max(d.x, max(d.y, d.z)), 0.0);
}

float map(vec3 p) {
    p = rotY(time) * rotX(time * 0.7) * p;
    return sdBox(p, vec3(0.6));
}

vec3 surfaceNormal(vec3 p) {
    vec2 e = vec2(0.001, 0.0);
    return normalize(vec3(
        map(p + e.xyy) - map(p - e.xyy),
        map(p + e.yxy) - map(p - e.yxy),
        map(p + e.yyx) - map(p - e.yyx)));
}

// faceColor returns a fixed colour per cube face by transforming the hit point
// into the cube's local (unrotated) space and picking the dominant axis.
vec3 faceColor(vec3 p) {
    vec3 lp = rotY(time) * rotX(time * 0.7) * p;
    vec3 a = abs(lp);
    if (a.x >= a.y && a.x >= a.z) {
        return lp.x > 0.0 ? vec3(0.90, 0.22, 0.25) : vec3(0.20, 0.70, 0.70);
    } else if (a.y >= a.z) {
        return lp.y > 0.0 ? vec3(0.32, 0.74, 0.36) : vec3(0.82, 0.32, 0.70);
    }
    return lp.z > 0.0 ? vec3(0.22, 0.46, 0.86) : vec3(0.95, 0.80, 0.26);
}

void main() {
    vec2 size = vec2(bounds[2] - bounds[0], bounds[3] - bounds[1]);
    vec2 center = vec2((bounds[0] + bounds[2]) * 0.5, frame.y - (bounds[1] + bounds[3]) * 0.5);
    vec2 uv = (gl_FragCoord.xy - center) / (0.5 * min(size.x, size.y));

    vec3 ro = vec3(0.0, 0.0, -3.0);
    vec3 rd = normalize(vec3(uv, 2.0));

    float t = 0.0;
    bool hit = false;
    vec3 p = ro;
    for (int i = 0; i < 64; i++) {
        p = ro + rd * t;
        float d = map(p);
        if (d < 0.001) {
            hit = true;
            break;
        }
        t += d;
        if (t > 6.0) {
            break;
        }
    }

    if (!hit) {
        discard;
    }

    vec3 n = surfaceNormal(p);
    vec3 lightDir = normalize(vec3(0.6, 0.8, -0.6));
    float diff = max(dot(n, lightDir), 0.0);
    vec3 col = faceColor(p) * (0.35 + 0.65 * diff);
    gl_FragColor = vec4(col, 1.0);
}
`

// cubeShaderSourceWGSL is the WebGPU variant of cubeShaderSource. Its pixel
// positions grow downwards, so the vertical is flipped to spin the same way.
const cubeShaderSourceWGSL = `
struct Params {
    time: f32,
}
@group(2) @binding(0) var<uniform> params: Params;

fn rot_x(a: f32) -> mat3x3<f32> {
    let c = cos(a);
    let s = sin(a);
    return mat3x3<f32>(1.0, 0.0, 0.0, 0.0, c, s, 0.0, -s, c);
}

fn rot_y(a: f32) -> mat3x3<f32> {
    let c = cos(a);
    let s = sin(a);
    return mat3x3<f32>(c, 0.0, -s, 0.0, 1.0, 0.0, s, 0.0, c);
}

fn sd_box(p: vec3<f32>, b: vec3<f32>) -> f32 {
    let d = abs(p) - b;
    return length(max(d, vec3<f32>(0.0))) + min(max(d.x, max(d.y, d.z)), 0.0);
}

// spin turns p into the cube's local (unrotated) space.
fn spin(p: vec3<f32>) -> vec3<f32> {
    return rot_y(params.time) * rot_x(params.time * 0.7) * p;
}

fn scene(p: vec3<f32>) -> f32 {
    return sd_box(spin(p), vec3<f32>(0.6));
}

fn surface_normal(p: vec3<f32>) -> vec3<f32> {
    let e = vec2<f32>(0.001, 0.0);
    return normalize(vec3<f32>(
        scene(p + e.xyy) - scene(p - e.xyy),
        scene(p + e.yxy) - scene(p - e.yxy),
        scene(p + e.yyx) - scene(p - e.yyx)));
}

// face_color returns a fixed colour per cube face, picking the dominant axis
// of the hit point in the cube's local space.
fn face_color(p: vec3<f32>) -> vec3<f32> {
    let lp = spin(p);
    let a = abs(lp);
    if (a.x >= a.y && a.x >= a.z) {
        return select(vec3<f32>(0.20, 0.70, 0.70), vec3<f32>(0.90, 0.22, 0.25), lp.x > 0.0);
    } else if (a.y >= a.z) {
        return select(vec3<f32>(0.82, 0.32, 0.70), vec3<f32>(0.32, 0.74, 0.36), lp.y > 0.0);
    }
    return select(vec3<f32>(0.95, 0.80, 0.26), vec3<f32>(0.22, 0.46, 0.86), lp.z > 0.0);
}

@fragment
fn main(@builtin(position) pos: vec4<f32>) -> @location(0) vec4<f32> {
    let b = fyne.bounds;
    let size = b.zw - b.xy;
    let center = (b.xy + b.zw) * 0.5;
    let uv = vec2<f32>(pos.x - center.x, center.y - pos.y) / (0.5 * min(size.x, size.y));

    let ro = vec3<f32>(0.0, 0.0, -3.0);
    let rd = normalize(vec3<f32>(uv, 2.0));

    var t = 0.0;
    var hit = false;
    var p = ro;
    for (var i = 0; i < 64; i++) {
        p = ro + rd * t;
        let d = scene(p);
        if (d < 0.001) {
            hit = true;
            break;
        }
        t += d;
        if (t > 6.0) {
            break;
        }
    }

    if (!hit) {
        discard;
    }

    let n = surface_normal(p);
    let light_dir = normalize(vec3<f32>(0.6, 0.8, -0.6));
    let diff = max(dot(n, light_dir), 0.0);
    return vec4<f32>(face_color(p) * (0.35 + 0.65 * diff), 1.0);
}
`

// newCubeShader builds a Shader that draws a spinning, shaded cube and starts
// the animation that advances its "time" uniform. The animation's Stop is
// registered with OnChangeFuncs so it pauses when leaving the canvas tutorial.
func newCubeShader() *canvas.Shader {
	shader := canvas.NewShader("demoSpinningCube", []byte(cubeShaderSource), []byte(cubeShaderSourceES))
	shader.SourceWGSL = []byte(cubeShaderSourceWGSL)

	anim := canvas.NewShaderAnimation(shader)
	anim.Start()
	OnChangeFuncs = append(OnChangeFuncs, anim.Stop)

	return shader
}
