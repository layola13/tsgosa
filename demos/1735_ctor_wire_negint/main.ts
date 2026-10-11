class F { n: i32 = 0; constructor() { this.n = -7; } }
function main(): i32 {
  const f = new F();
  console.log(f.n);
  return 0;
}
