function get(): i32 { return 5; }
class F { n: i32 = 0; constructor(k: i32) { this.n = get() + 1; } }
function main(): i32 {
  const f = new F(8);
  console.log(f.n);
  return 0;
}
