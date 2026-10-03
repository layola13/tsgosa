interface P {
  x: i32;
  y: i32;
}
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const c: P = { ...o, y: 20 };
  console.log(c.x + c.y);
  return 0;
}
