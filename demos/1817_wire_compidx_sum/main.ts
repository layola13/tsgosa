class C { n: i32 = 0; constructor(a: i32[]) { this.n = a[a.length - 1] + a[0]; } }
function main(): i32 {
  const c = new C([3, 4]);
  console.log(c.n);
  return 0;
}
