class F { n: i32 = 0; constructor(flag) { this.n = flag + 10; } }
function main(): i32 {
  const f = new F(true);
  console.log(f.n);
  return 0;
}
