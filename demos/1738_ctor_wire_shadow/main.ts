class F { n: i32 = 0; constructor(k: i32) { this.n = k + 1; } }
function main(): i32 {
  const k = 100;
  const f = new F(8);
  console.log(f.n);
  console.log(k);
  return 0;
}
