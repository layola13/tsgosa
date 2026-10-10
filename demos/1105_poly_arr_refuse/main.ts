class A {
  v(): i32 { return 1; }
}
class B extends A {
  v(): i32 { return 2; }
}
function main(): i32 {
  const a: A[] = [new A(), new B()];
  console.log(a[0].v() + a[1].v());
  return 0;
}
