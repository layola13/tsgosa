interface Bx { x: i32; }
function main(): i32 {
  const b: Bx = {x: 5};
  const {x: {y}} = b;
  console.log(y);
  return 0;
}
