interface I { get(): i32; }
class C implements I {
  get(): i32 { return 9; }
}
function main(): i32 {
  const c = new C();
  console.log(c.get());
  return 0;
}
