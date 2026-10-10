class A {
  v: i32 = 1;
}
class B extends A {
  w: i32 = 2;
}
function main(): i32 {
  const b = new B();
  console.log(b.v);
  console.log(b.w);
  return 0;
}
