class F { n: i32 = 0; constructor(k: i32) { this.n = k + 1; } }
function main(): i32 {
  const s: string = "live";
  const k: string = "x";
  const f = new F(8);
  console.log(f.n);
  console.log(k);
  console.log(s);
  return 0;
}
