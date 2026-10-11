class F { n: i32 = 0; rate: f64 = 0; s: string = ""; ok: boolean = false; constructor() { this.n = 2 + 3; this.rate = 1.5 * 2; this.s = "a" + "b"; this.ok = !false; } }
function main(): i32 {
  const f = new F();
  console.log(f.n);
  console.log(f.rate);
  console.log(f.s);
  console.log(f.ok ? 1 : 0);
  return 0;
}
