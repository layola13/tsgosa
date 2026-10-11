class C { n: i32 = 0; constructor(f: boolean) { this.n = f ? 10 : 20; } }
function main(): i32 {
  const c = new C(true);
  console.log(c.n);
  return 0;
}
