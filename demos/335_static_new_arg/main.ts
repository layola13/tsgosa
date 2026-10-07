class A {
  v: i32 = 5;
  static get(o: A): i32 {
    return o.v;
  }
}
function main(): number {
  return A.get(new A());
}
console.log(main());
