class B {
  v: number = 1;
}
class D extends B {
  w: number = 2;
}

export function main(): i32 {
  const d = new D();
  const b = new B();
  console.log(d instanceof B ? 1 : 0);
  console.log(b instanceof D ? 1 : 0);
  console.log(b instanceof B ? 1 : 0);
  console.log(1 instanceof B ? 1 : 0);
  console.log([1] instanceof Array ? 1 : 0);
  return 0;
}
