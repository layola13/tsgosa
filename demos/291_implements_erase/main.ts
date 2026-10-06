interface A { a: i32; run(): i32; }
class C implements A {
  a: i32 = 1;
}
class D implements Nope {
  d: i32 = 2;
}
function main(): i32 {
  const c = new C();
  const d = new D();
  console.log(c.a + d.d);
  return c.a + d.d;
}
console.log(main());
