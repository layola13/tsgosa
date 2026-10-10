function n(): i32 { return 2; }
function main(): i32 {
  let i = 0;
  do {
    i += 1;
  } while (i < n());
  console.log(i);
  return 0;
}
