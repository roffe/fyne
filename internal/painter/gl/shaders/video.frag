#version 110

// VideoFrames: a luma plane and chroma in two planes, or one holding U and V
// as luminance and alpha, turned and converted to RGB as they are drawn.
uniform sampler2D luma;
uniform sampler2D chroma;
uniform sampler2D chromaV;
uniform float turns;       // clockwise quarter turns
uniform float interleaved; // 1 when chroma holds U and V together
// 1 when samples take two channels, low byte first: luminance and alpha, or
// red and green then blue and alpha
uniform float wide;
// Planes are uploaded as wide as their rows are long. x: the picture's width
// over the texture's, y: the last texel centre in the picture.
uniform vec2 lumaFit;
uniform vec2 chromaFit;
// each channel is dot(row.xyz, yuv) + row.w
uniform vec4 rowR;
uniform vec4 rowG;
uniform vec4 rowB;
// x: 0 for SDR, 1 for PQ, 2 for HLG; y: the peak as a PQ signal; z, w: SDR
// white and where the curve down to it starts, as fractions of y
uniform vec4 hdr;

varying vec2 fragTexCoord;

// two bytes of a sample, as fractions of 255, to the sample as a fraction of 65535
const vec2 bytes = vec2(1.0 / 257.0, 256.0 / 257.0);

// SMPTE ST 2084's curve between signal and light, as a fraction of 10000 cd/m2
float pqLight(float e) {
    float p = pow(clamp(e, 0.0, 1.0), 1.0 / 78.84375);
    return pow(max(p - 0.8359375, 0.0) / (18.8515625 - 18.6875 * p), 1.0 / 0.1593017578125);
}

float pqSignal(float l) {
    float p = pow(max(l, 0.0), 0.1593017578125);
    return pow((0.8359375 + 18.8515625 * p) / (1.0 + 18.6875 * p), 78.84375);
}

// the light HLG's signal makes on a 1000 cd/m2 screen, as a fraction of 10000
vec3 hlgLight(vec3 e) {
    vec3 scene = mix((exp((e - 0.55991073) / 0.17883277) + 0.28466892) / 12.0, e * e / 3.0, vec3(lessThanEqual(e, vec3(0.5))));
    return scene * 0.1 * pow(max(dot(scene, vec3(0.2627, 0.6780, 0.0593)), 1e-6), 0.2);
}

// sdr tone maps e, an HDR signal, to SDR as VideoHDR in Go describes.
vec3 sdr(vec3 e) {
    vec3 l = hdr.x > 1.5 ? hlgLight(e) : vec3(pqLight(e.r), pqLight(e.g), pqLight(e.b));
    // BT.2020 primaries to BT.709's, by columns
    l = mat3(1.660491, -0.124550, -0.018151, -0.587641, 1.132900, -0.100579, -0.072850, -0.008349, 1.118730) * l;
    float m = max(l.r, max(l.g, l.b));
    float x = min(pqSignal(m) / hdr.y, 1.0);
    if (m > 0.0 && x > hdr.w) {
        float t = (x - hdr.w) / (1.0 - hdr.w);
        float t2 = t * t;
        float t3 = t2 * t;
        float s = (2.0 * t3 - 3.0 * t2 + 1.0) * hdr.w + (t3 - 2.0 * t2 + t) * (1.0 - hdr.w) + (3.0 * t2 - 2.0 * t3) * hdr.z;
        l *= pqLight(s * hdr.y) / m;
    }
    return pow(clamp(l * (10000.0 / 203.0), 0.0, 1.0), vec3(1.0 / 2.4));
}

void main() {
    vec2 p = fragTexCoord;
    if (turns > 2.5) {
        p = vec2(1.0 - p.y, p.x);
    } else if (turns > 1.5) {
        p = 1.0 - p;
    } else if (turns > 0.5) {
        p = vec2(p.y, 1.0 - p.x);
    }
    vec2 cp = vec2(min(p.x * chromaFit.x, chromaFit.y), p.y);
    vec4 l = texture2D(luma, vec2(min(p.x * lumaFit.x, lumaFit.y), p.y));
    vec4 c = texture2D(chroma, cp);
    vec4 cv = texture2D(chromaV, cp);
    vec3 yuv = vec3(l.r, c.r, interleaved > 0.5 ? c.a : cv.r);
    if (wide > 0.5) {
        yuv = vec3(dot(l.ra, bytes), interleaved > 0.5 ? vec2(dot(c.rg, bytes), dot(c.ba, bytes)) : vec2(dot(c.ra, bytes), dot(cv.ra, bytes)));
    }
    vec3 rgb = clamp(vec3(dot(rowR.xyz, yuv) + rowR.w, dot(rowG.xyz, yuv) + rowG.w, dot(rowB.xyz, yuv) + rowB.w), 0.0, 1.0);
    if (hdr.x > 0.5) {
        rgb = sdr(rgb);
    }
    gl_FragColor = vec4(rgb, 1.0);
}
