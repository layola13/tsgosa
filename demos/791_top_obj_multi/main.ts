interface P { x: i32; y: i32 }
const A: P = {x: 1, y: 2}, B: P = {x: 3, y: 4};
function main(): i32 {
  console.log(A.x + B.y);
  return 0;
}
