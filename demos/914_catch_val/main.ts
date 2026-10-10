function main(): i32 {
  try {
    throw 42;
  } catch (e) {
    console.log(e);
  }
  return 0;
}
