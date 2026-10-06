interface B { x: i32; }
class A {
  b: B = { x: 1 };
}
export default function(): i32 { return 1; }
export default class {
  v: i32 = 1;
}
function main(): i32 {
  const a = new A();
  const t = typeof 1;
  console.log(a.b.x);
  console.log(t);
  return a.b.x;
}
console.log(main());
