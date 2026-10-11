class C { n: i32 = 0; constructor(k: i32) { this.n = k > 0 ? 1 : 0; } }
function main(): i32 {
  const c = new C(5);
  console.log(c.n);
  return 0;
}
