class C { n: i32 = 0; constructor(a: readonly i32[]) { this.n = a.length; } }
function main(): i32 {
  const c = new C([1, 2, 3]);
  console.log(c.n);
  return 0;
}
