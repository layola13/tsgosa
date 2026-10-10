function a(): i32 { return 1; }
function b(): i32 { return 0; }
function main(): i32 {
  console.log(a() && b() ? 1 : 0);
  console.log(a() || b() ? 1 : 0);
  return 0;
}
