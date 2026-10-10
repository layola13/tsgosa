class A {
  static K = 3;
}
class B extends A {
}
function main(): i32 {
  console.log(B.K);
  return 0;
}
