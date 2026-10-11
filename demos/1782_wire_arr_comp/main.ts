class C { n: i32 = 0; constructor(a: i32[]) { this.n = a[0] + 1; } }
function main(): i32 {
  const c = new C([10, 20]);
  console.log(c.n);
  return 0;
}
