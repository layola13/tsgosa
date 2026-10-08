abstract class A {
  abstract v(): i32;
}
class B extends A {
  v(): i32 {
    return 6;
  }
}
function main(): i32 {
  const b = new B();
  console.log(b.v());
  return 0;
}
