function f(): i32 { return 7; }
function main(): i32 {
  console.log(f?.() ?? -1);
  return 0;
}
