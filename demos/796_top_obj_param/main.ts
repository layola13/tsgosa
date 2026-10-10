interface P { x: i32; y: i32 }
const O: P = {x: 4, y: 5};
function get(k: string): i32 {
  if (k == "x") {
    return O.x;
  }
  return O.y;
}
function main(): i32 {
  console.log(get("x") + get("y"));
  return 0;
}
