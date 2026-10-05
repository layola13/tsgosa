export interface Size { unit: number; verb: number; }

export function makeSize(u: number, v: number): Size {
  return { unit: u, verb: v };
}

export function getOrNull(M: Record<string, Size>, k: string): Size | null {
  return M[k] ?? null;
}

export function pick(a: Size, b: Size): Size {
  const x = a ?? b;
  return x;
}

export function unitOf(s: Size | null): number {
  if (s) {
    return s.unit;
  }
  return 0;
}

export function condPick(a: Size, b: Size): number {
  if (a ?? b) {
    return 1;
  }
  return 0;
}

export class Schema {
  unit: number;
  name: string;
  constructor(unit: number, name: string) {
    this.unit = unit;
    this.name = name;
  }
  check(s: string): number {
    if (this.name == s) {
      return this.unit;
    }
    return 0;
  }
  describe(prefix: string): string {
    return prefix + this.name;
  }
}

export function parsePort(M: Record<string, string>, k: string): number {
  const v = M[k];
  return parseInt(v, 10);
}

export function isLower(s: string): number {
  const re = /^[a-z]+$/;
  if (re.test(s)) {
    return 1;
  }
  return 0;
}

export function score(vals: number[], extra: number): number {
  let total = 0;
  for (const v of vals) {
    total = total + v;
  }
  total = total + Math.min(extra, 10);
  if (typeof extra == "number") {
    total = total + 1;
  }
  return total;
}

export function computeZod(): number {
  const a = makeSize(3, 4);
  const b = makeSize(30, 40);
  const M: Record<string, Size> = { alpha: { unit: 5, verb: 6 } };
  const hit = getOrNull(M, "alpha");
  const miss = getOrNull(M, "nope");
  let acc = 0;
  acc = acc + unitOf(hit) * 1;
  acc = acc + unitOf(miss) * 2;
  const p = pick(a, b);
  acc = acc + p.unit * 4;
  acc = acc + condPick(a, b) * 8;
  const sc = new Schema(7, "hi");
  acc = acc + sc.check("hi") * 16;
  const label = sc.describe("v:");
  if (label == "v:hi") {
    acc = acc + 32;
  }
  const P: Record<string, string> = { port: "8080" };
  acc = acc + parsePort(P, "port");
  acc = acc + isLower("hello") * 64;
  acc = acc + score([1, 2, 3], 5) * 128;
  return acc;
}
