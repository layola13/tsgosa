interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  switch (rest.y) { case 1: console.log(1); break; case 2: console.log(2); break; default: console.log(0); }
  return 0;
}
