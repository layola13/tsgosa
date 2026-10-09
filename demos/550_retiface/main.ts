interface R { x: i32; }
function f(): R { return { x: 3 }; }
function main(): i32 {
  console.log(f().x);
  return 0;
}
