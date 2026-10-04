function probe(): i32 {
  let e: i32 = 0;
  try {
    e = 41;
    throw e;
  } catch (err) {
    return err;
  }
  return 0;
}
function main(): i32 {
  console.log(probe());
  return 0;
}
