const fs = require('fs');
const path = require('path');

// --- Pseudo-Random Number Generator Setup ---
function xmur3(str) {
    for (var i = 0, h = 1779033703 ^ str.length; i < str.length; i++) {
        h = Math.imul(h ^ str.charCodeAt(i), 3432918353);
        h = (h << 13) | (h >>> 19);
    }
    return function () {
        h = Math.imul(h ^ (h >>> 16), 2246822507);
        h = Math.imul(h ^ (h >>> 13), 3266489909);
        return (h ^= h >>> 16) >>> 0;
    };
}

function mulberry32(a) {
    return function () {
        var t = (a += 0x6d2b79f5);
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
}

let R = null;
function seedRNG(seedStr) {
    const seed = xmur3(seedStr)();
    R = mulberry32(seed);
    return R;
}
function randomRange(min, max) { return min + R() * (max - min); }
function randomInt(min, max) { return Math.floor(randomRange(min, max)); }
function randomChoice(arr) { return arr[randomInt(0, arr.length)]; }
function randomR() { return R(); }

// --- CONCEPT 1: Ethereal Blob ---
function generateEtherealBlob(seedStr, numericSeed) {
    seedRNG(seedStr);
    const hue = randomInt(0, 360);
    const hue2 = (hue + randomChoice([60, 120, 180])) % 360;
    const bgLightness = randomInt(5, 15);
    const blobSize = randomRange(50, 80);
    const freqX = randomRange(0.01, 0.05).toFixed(3);
    const freqY = randomRange(0.01, 0.05).toFixed(3);
    const scale = randomInt(20, 100);

    return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200">
    <defs>
        <linearGradient id="bg-${numericSeed}" x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" stop-color="hsl(${hue}, 80%, ${bgLightness}%)" />
            <stop offset="100%" stop-color="#000" />
        </linearGradient>
        <radialGradient id="glow-${numericSeed}" cx="50%" cy="50%" r="50%">
            <stop offset="0%" stop-color="hsl(${hue}, 100%, 70%)" stop-opacity="0.8" />
            <stop offset="70%" stop-color="hsl(${hue2}, 100%, 50%)" stop-opacity="0.3" />
            <stop offset="100%" stop-color="hsl(${hue}, 100%, 20%)" stop-opacity="0" />
        </radialGradient>
        <filter id="displacementFilter-${numericSeed}">
            <feTurbulence type="fractalNoise" baseFrequency="${freqX} ${freqY}" numOctaves="3" result="noise" seed="${numericSeed}"/>
            <feDisplacementMap in="SourceGraphic" in2="noise" scale="${scale}" xChannelSelector="R" yChannelSelector="G"/>
        </filter>
    </defs>
    <rect width="200" height="200" fill="url(#bg-${numericSeed})" />
    <circle cx="100" cy="100" r="${blobSize}" fill="url(#glow-${numericSeed})" filter="url(#displacementFilter-${numericSeed})" />
    <circle cx="100" cy="100" r="${blobSize * 0.3}" fill="#fff" opacity="0.6" filter="blur(8px)" />
</svg>`;
}

// --- CONCEPT 2: Sacred Geometry (Golden Ratio) ---
function generateSacredGeometry(seedStr) {
    seedRNG(seedStr);
    let svgStr = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200" style="background-color: #0d1117">
    <g transform="translate(100, 100)">`;

    const PHI = 1.61803398875;
    const layers = randomInt(4, 9);
    const baseHue = randomInt(0, 360);
    const fibPoints = [3, 5, 8, 13, 21];

    for (let i = 0; i < layers; i++) {
        const radius = 95 / Math.pow(PHI, i);
        const points = randomChoice(fibPoints);
        const hue = (baseHue + i * 137.5) % 360;
        const stroke = `hsl(${hue}, 80%, 60%)`;
        const fill = randomR() > 0.5 ? "none" : `hsl(${hue}, 60%, 20%)`;
        const lineWidth = randomRange(0.5, 2.5);
        const rotationOffset = (i * 137.5 + randomRange(0, 30)) % 360;

        let pathStr = "";
        const shapeType = randomChoice(["polygon", "circles", "lines"]);
        
        for (let j = 0; j < points; j++) {
            const angle = (j / points) * Math.PI * 2 + (rotationOffset * Math.PI) / 180;

            if (shapeType === "polygon") {
                const px = Math.cos(angle) * radius;
                const py = Math.sin(angle) * radius;
                pathStr += (j === 0 ? "M" : "L") + ` ${px} ${py} `;
            } else if (shapeType === "circles") {
                const rCenter = radius / PHI;
                const px = Math.cos(angle) * rCenter;
                const py = Math.sin(angle) * rCenter;
                const cr = radius - rCenter;
                svgStr += `\n<circle cx="${px}" cy="${py}" r="${cr}" stroke="${stroke}" fill="none" stroke-width="${lineWidth}" />`;
            } else {
                const px = Math.cos(angle) * radius;
                const py = Math.sin(angle) * radius;
                const rIn = radius / PHI;
                const pxIn = Math.cos(angle) * rIn;
                const pyIn = Math.sin(angle) * rIn;
                svgStr += `\n<line x1="${pxIn}" y1="${pyIn}" x2="${px}" y2="${py}" stroke="${stroke}" stroke-width="${lineWidth}" />`;
            }
        }

        if (pathStr.length > 0) {
            pathStr += "Z";
            svgStr += `\n<path d="${pathStr}" stroke="${stroke}" fill="${fill}" stroke-width="${lineWidth}" opacity="0.8" />`;
        }
    }

    svgStr += `\n<circle cx="0" cy="0" r="${randomRange(2, 6)}" fill="hsl(${baseHue}, 100%, 80%)" />`;
    svgStr += `\n</g>\n</svg>`;
    return svgStr;
}

// --- CONCEPT 3: The Sigil ---
function generateSigil(seedStr) {
    seedRNG(seedStr);
    const gridSz = randomChoice([4, 5, 6, 7]);
    const boxSz = 140;
    const cellSz = boxSz / gridSz;
    const margin = (200 - boxSz) / 2;

    const hue = randomInt(0, 360);
    const isDark = randomR() > 0.5;
    const bg = isDark ? "#111" : "#eee";
    const fg = isDark ? `hsl(${hue}, 80%, 65%)` : `hsl(${hue}, 80%, 35%)`;

    let svgStr = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200" style="background-color: ${bg}">\n<g fill="${fg}">`;

    const numCols = Math.ceil(gridSz / 2);
    for (let y = 0; y < gridSz; y++) {
        for (let x = 0; x < numCols; x++) {
            if (randomR() > 0.4) {
                const cx = margin + x * cellSz;
                const cy = margin + y * cellSz;
                svgStr += `\n<rect x="${cx}" y="${cy}" width="${cellSz + 0.5}" height="${cellSz + 0.5}" />`;

                const mx = gridSz - 1 - x;
                if (mx !== x) {
                    const mcx = margin + mx * cellSz;
                    svgStr += `\n<rect x="${mcx}" y="${cy}" width="${cellSz + 0.5}" height="${cellSz + 0.5}" />`;
                }
            }
        }
    }
    svgStr += `\n</g>`;

    if (randomR() > 0.5) {
        svgStr += `\n<rect x="${margin - 10}" y="${margin - 10}" width="${boxSz + 20}" height="${boxSz + 20}" fill="none" stroke="${fg}" stroke-width="2" />`;
    }
    if (randomR() > 0.7) {
        svgStr += `\n<circle cx="100" cy="100" r="${boxSz * 0.75}" fill="none" stroke="${fg}" stroke-width="1.5" stroke-dasharray="5,5" />`;
    }

    svgStr += `\n</svg>`;
    return svgStr;
}

const concepts = [
    { name: "concept1_ethereal", fn: generateEtherealBlob, usesNumericSeed: true },
    { name: "concept2_sacred", fn: generateSacredGeometry, usesNumericSeed: false },
    { name: "concept3_sigil", fn: generateSigil, usesNumericSeed: false },
];

const seeds = ["genesis-0", "genesis-1", "example-hash-xyz", "some-other-hash-123", "random-abc"];

const outDir = path.join(__dirname, "generated_svgs");
if (!fs.existsSync(outDir)) fs.mkdirSync(outDir);

concepts.forEach(concept => {
    seeds.forEach((seed, i) => {
        let content = "";
        if (concept.usesNumericSeed) {
            content = concept.fn(seed, xmur3(seed)());
        } else {
            content = concept.fn(seed);
        }
        
        fs.writeFileSync(path.join(outDir, \`\${concept.name}_seed_\${i}.svg\`), content);
        console.log(\`Generated \${concept.name}_seed_\${i}.svg\`);
    });
});

console.log('All SVGs generated successfully in "generated_svgs" folder.');
