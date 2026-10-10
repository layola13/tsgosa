interface Box { w: i32; h: i32; }
function main(): i32 {
  const b: Box = { w: 3, h: 4 };
  console.log(b.w * b.h);
  return 0;
}
