// Slug text, see slug.frag. The #version line is prepended per build.

attribute vec2 vert;
attribute vec2 vertTexCoord;
attribute vec4 vertColor;
attribute float glyphBase;

varying vec2 fragCoord;
varying vec4 fragColor;
varying float fragBase;

void main() {
    fragCoord = vertTexCoord;
    fragColor = vertColor;
    fragBase = glyphBase;

    gl_Position = vec4(vert, 0, 1);
}
