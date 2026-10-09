interface Q {
  x: i32;
}
interface A {
  x: i32;
}
interface B {
  x: i32;
  y: i32;
}
interface P {
  a: i32;
  b: i32;
}
function main(): i32 {
  const o: Q = { x: 5 };
  console.log(JSON.stringify(o));
  const a: A = { x: 1 };
  const b: B = { ...a, y: 2 };
  console.log(b.x);
  console.log(b.y);
  const p: P = { a: 1, b: 2 };
  console.log(Object.keys(p).length);
  return 0;
}
