class A {
  v(): i32 {
    return 1;
  }
}
class B extends A {
  override v(): i32 {
    return 2;
  }
}
function main(): i32 {
  const b = new B();
  console.log(b.v());
  return 0;
}
