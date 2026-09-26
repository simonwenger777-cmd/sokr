// LZW fold used by the page. Same bit layout as the URI mode of lz-string,
// so a string packed here unpacks in the browser without a server.
const uriAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+-$";

function compressURI(input) {
  if (input == null || input === "") return "";
  return lzwCompress(input, 6, (code) => uriAlphabet.charAt(code));
}

function decompressURI(input) {
  if (input == null) return "";
  if (input === "") return "";
  const cleaned = input.replace(/ /g, "+");
  return lzwDecompress(cleaned.length, 32, (index) => uriAlphabet.indexOf(cleaned.charAt(index)));
}

function pushBit(state, bit) {
  state.val = (state.val << 1) | bit;
  if (state.position === state.bits - 1) {
    state.out.push(state.getChar(state.val));
    state.val = 0;
    state.position = 0;
  } else {
    state.position++;
  }
}

function pushBitsLSB(state, value, count) {
  for (let i = 0; i < count; i++) {
    pushBit(state, value & 1);
    value >>= 1;
  }
}

function grow(state) {
  state.enlargeIn--;
  if (state.enlargeIn === 0) {
    state.enlargeIn = 1 << state.numBits;
    state.numBits++;
  }
}

function writeNewSymbol(state, word) {
  const code = word.charCodeAt(0);
  if (code < 256) {
    pushBitsLSB(state, 0, state.numBits);
    pushBitsLSB(state, code, 8);
  } else {
    pushBitsLSB(state, 1, state.numBits);
    pushBitsLSB(state, code, 16);
  }
  grow(state);
}

function lzwCompress(uncompressed, bitsPerChar, getChar) {
  const dictionary = new Map();
  const fresh = new Set();
  let dictSize = 3;
  let numBits = 2;
  let enlargeIn = 2;
  let w = "";
  const state = { val: 0, position: 0, bits: bitsPerChar, out: [], getChar, numBits, enlargeIn };

  const sync = () => {
    state.numBits = numBits;
    state.enlargeIn = enlargeIn;
  };
  const pull = () => {
    numBits = state.numBits;
    enlargeIn = state.enlargeIn;
  };

  for (let i = 0; i < uncompressed.length; i++) {
    const c = uncompressed.charAt(i);
    if (!dictionary.has(c)) {
      dictionary.set(c, dictSize++);
      fresh.add(c);
    }
    const wc = w + c;
    if (dictionary.has(wc)) {
      w = wc;
      continue;
    }
    sync();
    if (fresh.has(w)) {
      writeNewSymbol(state, w);
      fresh.delete(w);
    } else {
      pushBitsLSB(state, dictionary.get(w), numBits);
    }
    pull();
    grow(state);
    pull();
    dictionary.set(wc, dictSize++);
    w = c;
  }

  if (w !== "") {
    sync();
    if (fresh.has(w)) writeNewSymbol(state, w);
    else pushBitsLSB(state, dictionary.get(w), numBits);
    pull();
    grow(state);
    pull();
  }

  sync();
  pushBitsLSB(state, 2, numBits);
  while (true) {
    state.val <<= 1;
    if (state.position === bitsPerChar - 1) {
      state.out.push(getChar(state.val));
      break;
    }
    state.position++;
  }
  return state.out.join("");
}

function readBits(data, count) {
  let bits = 0;
  let power = 1;
  const max = 1 << count;
  while (power !== max) {
    const bit = data.val & data.position;
    data.position >>= 1;
    if (data.position === 0) {
      data.position = data.reset;
      data.val = data.get(data.index++);
    }
    bits |= (bit > 0 ? 1 : 0) * power;
    power <<= 1;
  }
  return bits;
}

function lzwDecompress(length, resetValue, getNext) {
  const dictionary = [];
  let enlargeIn = 4;
  let dictSize = 4;
  let numBits = 3;
  let result = "";
  const data = { val: getNext(0), position: resetValue, index: 1, reset: resetValue, get: getNext };

  for (let i = 0; i < 3; i++) dictionary[i] = String(i);

  let marker = readBits(data, 2);
  let c;
  if (marker === 0) c = String.fromCharCode(readBits(data, 8));
  else if (marker === 1) c = String.fromCharCode(readBits(data, 16));
  else return "";

  dictionary[3] = c;
  let w = c;
  result += c;

  while (true) {
    if (data.index > length) return "";
    let code = readBits(data, numBits);
    if (code === 0) {
      dictionary[dictSize++] = String.fromCharCode(readBits(data, 8));
      code = dictSize - 1;
      enlargeIn--;
    } else if (code === 1) {
      dictionary[dictSize++] = String.fromCharCode(readBits(data, 16));
      code = dictSize - 1;
      enlargeIn--;
    } else if (code === 2) {
      return result;
    }

    if (enlargeIn === 0) {
      enlargeIn = 1 << numBits;
      numBits++;
    }

    let entry;
    if (dictionary[code] !== undefined) entry = dictionary[code];
    else if (code === dictSize) entry = w + w.charAt(0);
    else return null;

    result += entry;
    dictionary[dictSize++] = w + entry.charAt(0);
    enlargeIn--;
    w = entry;
    if (enlargeIn === 0) {
      enlargeIn = 1 << numBits;
      numBits++;
    }
  }
}

function bytesToB64url(bytes) {
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x2000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x2000));
  }
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function b64urlToBytes(text) {
  let s = text.replace(/-/g, "+").replace(/_/g, "/");
  while (s.length % 4) s += "=";
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function gzipBytes(bytes) {
  const stream = new Blob([bytes]).stream().pipeThrough(new CompressionStream("gzip"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

async function gunzipBytes(bytes) {
  const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

async function unfold(packed) {
  if (packed == null) throw new Error("пусто");
  const text = String(packed).trim();
  if (text.startsWith("1.")) {
    const out = decompressURI(text.slice(2));
    if (out == null) throw new Error("строка повреждена");
    return out;
  }
  if (text.startsWith("2.") || text.startsWith("z.")) {
    const bytes = b64urlToBytes(text.slice(2));
    return new TextDecoder().decode(await gunzipBytes(bytes));
  }
  if (text.startsWith("r.")) {
    return new TextDecoder().decode(b64urlToBytes(text.slice(2)));
  }
  return text;
}

async function fold(text) {
  if (!text) throw new Error("Сначала вставьте строку");
  const lz = "1." + compressURI(text);
  const gz = "2." + bytesToB64url(await gzipBytes(new TextEncoder().encode(text)));
  const candidates = [lz, gz].sort((a, b) => a.length - b.length);
  let best = null;
  for (const packed of candidates) {
    if ((await unfold(packed)) === text) {
      best = packed;
      break;
    }
  }
  if (!best) throw new Error("не удалось свернуть");
  const shorter = best.length < text.length;
  return { packed: shorter ? best : text, link: best, shorter };
}

const foldApi = { fold, unfold, compressURI, decompressURI };
if (typeof module !== "undefined" && module.exports) module.exports = foldApi;
