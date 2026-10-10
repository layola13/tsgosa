class A {
  v(): i32 { return 1; }
}
class B extends A {
  v(): i32 { return 2; }
}
class Cc extends B {
  v(): i32 { return 3; }
}
function main(): i32 {
  const c = new Cc();
  console.log(c.v());
  return 0;
}
