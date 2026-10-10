interface P { x: i32; y: i32 }
let O: P = {x: 6, y: 7};
function main(): i32 {
  console.log(O.x + O.y);
  return 0;
}
