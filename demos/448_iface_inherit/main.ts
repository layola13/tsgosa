interface A {
  x: i32;
}
interface B extends A {
  y: i32;
}
function main(): i32 {
  const o: B = { x: 3, y: 4 };
  console.log(o.x + o.y);
  return 0;
}
